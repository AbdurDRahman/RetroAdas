# Test data: the M-2 motorway through the Salt Range

`salt_range_m2.geojson` holds real road shapes for the road-following search
tests (`docs/design.md` section 5). GitHub shows a `.geojson` file as a map.

**Map data © OpenStreetMap contributors**, available under the Open Database
License (ODbL) 1.0: https://www.openstreetmap.org/copyright

## What is in it

| Road | What it is |
|---|---|
| `m2_southbound` | About 5 kilometers of the southbound carriageway, the descent south of Kallar Kahar. The points are in driving order. |
| `m2_northbound` | The northbound carriageway along the same stretch, 13 to 25 meters from the other. The points are in driving order. |
| `service_road_1`, `service_road_2` | Two short service roads (about 150 meters each) that touch the motorway. |

Coordinates are `[longitude, latitude]`, as GeoJSON requires.

This stretch was chosen because it is the most winding 5 kilometers of the M-2
in the area: in its first kilometer the distance by road is 2.2 times the
straight line. No other mapped road runs beside it; the nearest is about 680
meters away.

## How it was made (2026-11-15)

1. Downloaded from OpenStreetMap's public query service (Overpass) with:
   `way["highway"](32.645,72.730,32.690,72.765); out geom;` and the same for
   the larger roads of the area `(32.66,72.70,32.80,72.95)`.
2. The motorway's pieces (`ref = M-2`) were joined end to end into one line per
   carriageway, and the most winding 5 kilometers were cut out.
3. Nothing was moved, smoothed or added.

The tests do not download anything; they read this file.
