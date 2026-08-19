// Simulates one "virtual device" driving and polling GET /v1/hazards the
// way a real Jetson unit / mobile app would: distance-triggered, not
// timer-triggered (see api_design.md design principles).
//
// Run once the GET /v1/hazards endpoint is wired up:
//   k6 run -e BASE_URL=http://localhost:8080 load-tests/k6/simulate_device_polling.js

import http from 'k6/http';
import { check, sleep } from 'k6';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const DEVICE_ID = __ENV.DEVICE_ID || 'veh-k6-sim-0001';
const API_KEY = __ENV.API_KEY || 'replace-with-a-seeded-test-key';

export const options = {
  vus: 10,        // 10 simulated vehicles
  duration: '30s',
};

// A crude straight-line "drive": start point + fixed heading, advancing by
// ~300m of lat/lng per iteration to mimic the client's distance-triggered
// poll trigger. Good enough for load shape; not meant to be geographically
// precise.
let lat = 33.6844;
let lng = 73.0479;
const headingDeg = 142.0;

export default function () {
  const params = {
    headers: {
      'X-Device-Id': DEVICE_ID,
      'X-Api-Key': API_KEY,
    },
  };

  const url = `${BASE_URL}/v1/hazards?lat=${lat}&lng=${lng}&heading_deg=${headingDeg}&speed_kmh=38.0&radius_m=1000`;
  const res = http.get(url, params);

  check(res, {
    'status is 200': (r) => r.status === 200,
  });

  // crude ~300m step (very rough conversion, fine for load simulation)
  lat += 0.0027;

  sleep(1); // stand-in for "time between distance-triggered polls"
}
