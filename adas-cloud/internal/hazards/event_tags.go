package hazards

import (
	"slices"
	"time"

	"github.com/AbdurDRahman/RetroAdas/adas-cloud/internal/devices"
)

// Where a report came from. The server derives it from the device type
// (SourceForDeviceType); a client never sends it.
const (
	SourceAuto   = "auto"   // a detector (the Jetson unit)
	SourceManual = "manual" // a person (the mobile app)
)

// EventTag is the server's rule set for one kind of hazard
// (docs/design.md section 4).
type EventTag struct {
	Name string

	// TimeToLive is how long a hazard stays live after its last confirmation.
	TimeToLive time.Duration

	// DeduplicationRadiusMeters is how close a new report must be to an
	// existing hazard of this tag to count as the same hazard.
	DeduplicationRadiusMeters float64

	// AllowedDeviceTypes lists which kinds of device may report this tag.
	AllowedDeviceTypes []string

	// DirectionMatters is true when the hazard sits on one side of the road, so
	// a report from oncoming traffic is a different hazard (section 6). False
	// for hazards that affect both directions.
	DirectionMatters bool
}

// eventTagRegistry is the whole registry. It lives in Go, not in a database
// table (decided in C-001): the API is the only writer of hazards, so the
// code is the gatekeeper. Changing a value or adding a tag is a code change
// and a redeploy. The values are checked by event_tags_test.go.
var eventTagRegistry = []EventTag{
	{
		Name:                      "accident",
		TimeToLive:                90 * time.Minute,
		DeduplicationRadiusMeters: 150,
		AllowedDeviceTypes:        []string{devices.DeviceTypeJetsonUnit, devices.DeviceTypeMobileApp},
		DirectionMatters:          false,
	},
	{
		Name:                      "pothole",
		TimeToLive:                14 * 24 * time.Hour,
		DeduplicationRadiusMeters: 25,
		AllowedDeviceTypes:        []string{devices.DeviceTypeJetsonUnit, devices.DeviceTypeMobileApp},
		DirectionMatters:          true,
	},
	{
		Name:                      "checkpost",
		TimeToLive:                4 * time.Hour,
		DeduplicationRadiusMeters: 100,
		AllowedDeviceTypes:        []string{devices.DeviceTypeMobileApp},
		DirectionMatters:          false,
	},
	{
		Name:                      "debris",
		TimeToLive:                24 * time.Hour,
		DeduplicationRadiusMeters: 50,
		AllowedDeviceTypes:        []string{devices.DeviceTypeMobileApp},
		DirectionMatters:          true,
	},
}

// LookUpEventTag returns the rules for a tag name. The second value is false
// for a name that isn't in the registry. Names are exact: "Pothole" is unknown.
func LookUpEventTag(name string) (EventTag, bool) {
	// A loop over four entries is as fast as anything else here, and keeps the
	// registry a plain readable list.
	for _, eventTag := range eventTagRegistry {
		if eventTag.Name == name {
			return eventTag, true
		}
	}
	return EventTag{}, false
}

// AllowsDeviceType reports whether a device of the given type may report this tag.
func (eventTag EventTag) AllowsDeviceType(deviceType string) bool {
	return slices.Contains(eventTag.AllowedDeviceTypes, deviceType)
}

// SourceForDeviceType turns a device type into the source of its reports. The
// second value is false for a type the system doesn't know.
func SourceForDeviceType(deviceType string) (string, bool) {
	switch deviceType {
	case devices.DeviceTypeJetsonUnit:
		return SourceAuto, true
	case devices.DeviceTypeMobileApp:
		return SourceManual, true
	default:
		return "", false
	}
}
