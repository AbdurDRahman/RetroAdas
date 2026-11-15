package hazards

import (
	"cmp"
	"database/sql"
	"fmt"
	"slices"
	"time"

	"github.com/lib/pq"
)

// MaximumHazardsPerSearch is the most hazards one search returns
// (docs/design.md section 8). When there are more, the nearest are kept.
const MaximumHazardsPerSearch = 100

// MinimumSpeedForTrustedHeadingKilometersPerHour is the speed below which the
// vehicle's heading is not used (docs/design.md section 5). A positioning
// receiver works out the heading from movement, so a car standing in a jam
// can report any direction.
const MinimumSpeedForTrustedHeadingKilometersPerHour = 5

// SearchQuery is what a vehicle sends with GET /v1/hazards (docs/design.md
// section 8): only where it is, which way it drives and how fast. It never
// sends a radius. The server works that out from the speed, so no device can
// ask for a huge circle.
type SearchQuery struct {
	Latitude               float64
	Longitude              float64
	HeadingDegrees         float64
	SpeedKilometersPerHour float64
}

// NearbyHazard is one hazard in the answer to a search.
type NearbyHazard struct {
	ID        int64
	EventTag  string
	Latitude  float64
	Longitude float64
	// DistanceMeters is measured from the vehicle's own position, not from
	// the center of the search circle, so a device can say "pothole in 95 meters".
	DistanceMeters float64
	// Confidence is the detector's score. It is nil for a person's report.
	Confidence    *float64
	Confirmations int
	// MatchedBy says how the search found this hazard: MatchedByRoad or
	// MatchedByStraightAhead.
	MatchedBy string
}

// How a search found a hazard (docs/design.md section 5).
const (
	// MatchedByRoad: one of the hazard's approach paths passes the vehicle in
	// its direction of travel. The hazard is on the vehicle's road.
	MatchedByRoad = "road"
	// MatchedByStraightAhead: the hazard lies inside the circle ahead of the
	// vehicle. It may be on another road.
	MatchedByStraightAhead = "straight_ahead"
)

// SearchResult is the answer to a search.
type SearchResult struct {
	Hazards []NearbyHazard

	// The vehicle polls again after driving NextPollDistanceMeters or after
	// FallbackTime, whichever comes first. These two are the only polling
	// values a device ever receives, so the rules can change on the server
	// without updating any device.
	NextPollDistanceMeters float64
	FallbackTime           time.Duration
}

// FindNearby returns the live hazards ahead of the vehicle (docs/design.md
// section 5). It looks in two ways and joins the results:
//
//   - along the road: hazards whose approach paths pass the vehicle in its
//     direction of travel (road_following_search.go). These follow curves.
//   - straight ahead: hazards inside a circle ahead of the vehicle. This finds
//     hazards that have no matching path yet.
//
// A vehicle that is nearly standing still gets only a circle around itself,
// without any direction rule, because its heading can't be trusted.
func FindNearby(transaction *sql.Tx, pollingRules PollingRules, searchQuery SearchQuery) (SearchResult, error) {
	vehicleIsSlow := searchQuery.SpeedKilometersPerHour < MinimumSpeedForTrustedHeadingKilometersPerHour

	hazardsStraightAhead, err := findHazardsStraightAhead(transaction, pollingRules, searchQuery, vehicleIsSlow)
	if err != nil {
		return SearchResult{}, err
	}

	// Following a road needs to know which way the vehicle is going along it.
	var hazardsOnRoad []NearbyHazard
	if !vehicleIsSlow {
		hazardsOnRoad, err = findHazardsOnRoad(transaction, searchQuery)
		if err != nil {
			return SearchResult{}, err
		}
	}

	return SearchResult{
		Hazards:                joinSearchResults(hazardsOnRoad, hazardsStraightAhead),
		NextPollDistanceMeters: pollingRules.PollDistanceMeters(searchQuery.SpeedKilometersPerHour),
		FallbackTime:           pollingRules.FallbackTime,
	}, nil
}

// joinSearchResults makes one list of the two searches: nearest first, at most
// MaximumHazardsPerSearch. A hazard found both ways is kept once, as the road
// match, because the distance by road is the real one.
func joinSearchResults(hazardsOnRoad []NearbyHazard, hazardsStraightAhead []NearbyHazard) []NearbyHazard {
	foundOnRoad := map[int64]bool{}
	for _, hazard := range hazardsOnRoad {
		foundOnRoad[hazard.ID] = true
	}

	joined := slices.Clone(hazardsOnRoad)
	for _, hazard := range hazardsStraightAhead {
		if !foundOnRoad[hazard.ID] {
			joined = append(joined, hazard)
		}
	}

	// Nearest first. The id settles two hazards at the same distance, so the
	// same search always gives the same order.
	slices.SortFunc(joined, func(first NearbyHazard, second NearbyHazard) int {
		return cmp.Or(
			cmp.Compare(first.DistanceMeters, second.DistanceMeters),
			cmp.Compare(first.ID, second.ID),
		)
	})
	if len(joined) > MaximumHazardsPerSearch {
		joined = joined[:MaximumHazardsPerSearch]
	}
	return joined
}

// findHazardsStraightAhead is the circle search. The circle's center lies one
// lookahead distance ahead of the vehicle and its radius is the search radius,
// so it covers from the vehicle to twice the lookahead distance ahead, and
// nothing behind.
//
// Hazards on the other side of the road are left out: for tags where direction
// matters, a hazard recorded by traffic driving the opposite way is not this
// vehicle's hazard.
func findHazardsStraightAhead(transaction *sql.Tx, pollingRules PollingRules, searchQuery SearchQuery, vehicleIsSlow bool) ([]NearbyHazard, error) {
	// Two distances, each with its own name. The design makes them equal
	// today (section 5), but they mean different things.
	lookaheadDistanceMeters := pollingRules.LookaheadDistanceMeters(searchQuery.SpeedKilometersPerHour)
	searchRadiusMeters := pollingRules.SearchRadiusMeters(searchQuery.SpeedKilometersPerHour)

	// The normal search applies the direction rule to the tags that use it.
	eventTagNamesToFilterByDirection := eventTagNamesWhereDirectionMatters()

	// A slow vehicle does not look ahead: the circle stays on the vehicle
	// itself, and no tag is filtered by direction. The radius is left as it is
	// (at this speed, the minimum).
	if vehicleIsSlow {
		lookaheadDistanceMeters = 0
		// An empty list, not nil: nil would reach Postgres as "unknown" and
		// the direction rule would quietly stay on.
		eventTagNamesToFilterByDirection = []string{}
	}

	// The inner SELECT runs once and gives two points: the vehicle, and the
	// circle's center (ST_Project moves a point a distance in a compass
	// direction, which it wants in radians). PostGIS points are
	// (longitude, latitude), the opposite of how people usually say it.
	// "::double precision" tells Postgres the type of a parameter; without it
	// PostGIS can't choose between its several versions of ST_Project.
	rows, err := transaction.Query(
		`SELECT hazards.id, hazards.event_tag,
		        ST_Y(hazards.location::geometry), ST_X(hazards.location::geometry),
		        ST_Distance(hazards.location, search.vehicle_position) AS distance_meters,
		        hazards.confidence, hazards.confirmations
		 FROM hazards,
		      (SELECT vehicle_position,
		              ST_Project(vehicle_position, $8::double precision, radians($4::double precision)) AS circle_center
		       FROM (SELECT ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography AS vehicle_position) AS vehicle
		      ) AS search
		 WHERE hazards.expires_at > now()
		   AND ST_DWithin(hazards.location, search.circle_center, $3::double precision)
		   -- The direction rule (section 6). A hazard is kept when its tag does
		   -- not use direction, or it has no heading, or its heading is close
		   -- enough to the vehicle's. The formula gives the smaller angle
		   -- between two compass directions, so 350 and 10 are 20 apart, not 340.
		   AND (
		        NOT (hazards.event_tag = ANY($6))
		     OR hazards.heading_degrees IS NULL
		     OR 180 - abs(abs(hazards.heading_degrees - $4::double precision) - 180) <= $7::double precision
		   )
		 ORDER BY distance_meters, hazards.id
		 LIMIT $5`,
		searchQuery.Longitude, searchQuery.Latitude, searchRadiusMeters, searchQuery.HeadingDegrees,
		MaximumHazardsPerSearch,
		pq.Array(eventTagNamesToFilterByDirection), MaximumSameDirectionHeadingDifferenceDegrees,
		lookaheadDistanceMeters,
	)
	if err != nil {
		return nil, fmt.Errorf("search for hazards straight ahead: %w", err)
	}
	return readNearbyHazards(rows, MatchedByStraightAhead)
}

// readNearbyHazards turns the rows of either search into hazards, and marks
// each with how it was found. Both searches select the same seven columns.
func readNearbyHazards(rows *sql.Rows, matchedBy string) ([]NearbyHazard, error) {
	defer rows.Close()

	var nearbyHazards []NearbyHazard
	for rows.Next() {
		hazard := NearbyHazard{MatchedBy: matchedBy}
		err := rows.Scan(&hazard.ID, &hazard.EventTag, &hazard.Latitude, &hazard.Longitude,
			&hazard.DistanceMeters, &hazard.Confidence, &hazard.Confirmations)
		if err != nil {
			return nil, fmt.Errorf("read a hazard from the search: %w", err)
		}
		nearbyHazards = append(nearbyHazards, hazard)
	}
	// rows.Next also stops when the connection breaks halfway. Without this
	// check a broken search would look like a short, successful one.
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read the search's hazards: %w", err)
	}
	return nearbyHazards, nil
}
