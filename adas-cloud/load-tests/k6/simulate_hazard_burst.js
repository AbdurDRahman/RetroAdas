// Simulates several "virtual devices" independently detecting and reporting
// the same real-world hazard within seconds of each other — exercises the
// dedup-on-write path (database_schema.md Section 4) from outside the API,
// as a real fleet of vehicles hitting the same pothole would.
//
// Run once POST /v1/hazards is wired up:
//   k6 run -e BASE_URL=http://localhost:8080 load-tests/k6/simulate_hazard_burst.js

import http from 'k6/http';
import { check } from 'k6';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const API_KEY = __ENV.API_KEY || 'replace-with-a-seeded-test-key';

export const options = {
  vus: 5,     // 5 "vehicles" driving past the same pothole
  iterations: 5,
};

// All 5 virtual devices report a hazard at (roughly) the same coordinates,
// well within the 100m dedup radius, to trigger confirmation-not-insert.
const HAZARD_LAT = 33.6850;
const HAZARD_LNG = 73.0490;

export default function () {
  const deviceID = `veh-k6-burst-${__VU}`;

  const payload = JSON.stringify({
    event_tag: 'pothole',
    lat: HAZARD_LAT + (Math.random() - 0.5) * 0.00005, // a few metres of jitter
    lng: HAZARD_LNG + (Math.random() - 0.5) * 0.00005,
    heading_deg: 140.0,
    confidence: 0.85 + Math.random() * 0.1,
  });

  const params = {
    headers: {
      'Content-Type': 'application/json',
      'X-Device-Id': deviceID,
      'X-Api-Key': API_KEY,
    },
  };

  const res = http.post(`${BASE_URL}/v1/hazards`, payload, params);

  check(res, {
    'status is 200 or 201': (r) => r.status === 200 || r.status === 201,
  });
}
