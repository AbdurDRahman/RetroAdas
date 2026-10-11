// Simulates several "virtual devices" independently detecting and reporting
// the same real-world hazard within seconds of each other — exercises the
// deduplication-on-write path (docs/design.md section 6) from outside the API,
// as a real fleet of vehicles hitting the same pothole would. JSON field names
// follow docs/design.md section 12.
//
// Run once POST /v1/hazards is wired up:
//   k6 run -e BASE_URL=http://localhost:8080 load-tests/k6/simulate_hazard_burst.js

import http from 'k6/http';
import { check } from 'k6';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const API_KEY = __ENV.API_KEY || 'replace-with-a-seeded-test-key';

// `vus` (virtual users) is k6's own option name — it can't be renamed.
export const options = {
  vus: 5,     // 5 "vehicles" driving past the same pothole
  iterations: 5,
};

// All 5 virtual devices report a hazard at (roughly) the same coordinates,
// well within the 100 meter deduplication radius, to trigger
// confirmation-not-insert.
const HAZARD_LATITUDE = 33.6850;
const HAZARD_LONGITUDE = 73.0490;

// About ±2.5 meters of random offset, like real GPS noise between vehicles.
const JITTER_DEGREES = 0.00005;

export default function () {
  // __VU is k6's built-in number of the current virtual user (1, 2, 3, ...).
  const deviceID = `vehicle-k6-burst-${__VU}`;

  const requestBody = JSON.stringify({
    event_tag: 'pothole',
    latitude: HAZARD_LATITUDE + (Math.random() - 0.5) * JITTER_DEGREES,
    longitude: HAZARD_LONGITUDE + (Math.random() - 0.5) * JITTER_DEGREES,
    heading_degrees: 140.0,
    confidence: 0.85 + Math.random() * 0.1,
  });

  const requestOptions = {
    headers: {
      'Content-Type': 'application/json',
      'X-Device-Id': deviceID,
      'X-Api-Key': API_KEY,
    },
  };

  const response = http.post(`${BASE_URL}/v1/hazards`, requestBody, requestOptions);

  check(response, {
    'status is 200 or 201': (checkedResponse) => checkedResponse.status === 200 || checkedResponse.status === 201,
  });
}
