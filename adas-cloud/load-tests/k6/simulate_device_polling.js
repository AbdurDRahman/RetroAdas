// Simulates one "virtual device" driving and polling GET /v1/hazards the
// way a real Jetson unit / mobile app would. Query parameter names follow
// docs/design.md section 12.
//
// Still the old fixed model (a step of about 300 meters per poll, one poll per
// second); C-113 switches it to the speed-based polling rule in
// docs/design.md section 5.
//
// Run once the GET /v1/hazards endpoint is wired up:
//   k6 run -e BASE_URL=http://localhost:8080 load-tests/k6/simulate_device_polling.js

import http from 'k6/http';
import { check, sleep } from 'k6';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const DEVICE_ID = __ENV.DEVICE_ID || 'vehicle-k6-simulation-0001';
const API_KEY = __ENV.API_KEY || 'replace-with-a-seeded-test-key';

// `vus` (virtual users) is k6's own option name — it can't be renamed.
export const options = {
  vus: 10,        // 10 simulated vehicles
  duration: '30s',
};

// A crude straight-line "drive": start point + fixed heading, advancing about
// 300 meters of latitude per iteration. Good enough for load shape; not meant
// to be geographically precise.
let latitude = 33.6844;
let longitude = 73.0479;
const HEADING_DEGREES = 142.0;
const SPEED_KILOMETERS_PER_HOUR = 38.0;
const RADIUS_METERS = 1000;

// One degree of latitude is about 111 kilometers, so 0.0027 degrees is about 300 meters.
const LATITUDE_STEP_DEGREES = 0.0027;

export default function () {
  const requestOptions = {
    headers: {
      'X-Device-Id': DEVICE_ID,
      'X-Api-Key': API_KEY,
    },
  };

  const url = `${BASE_URL}/v1/hazards`
    + `?latitude=${latitude}&longitude=${longitude}`
    + `&heading_degrees=${HEADING_DEGREES}`
    + `&speed_kilometers_per_hour=${SPEED_KILOMETERS_PER_HOUR}`
    + `&radius_meters=${RADIUS_METERS}`;
  const response = http.get(url, requestOptions);

  check(response, {
    'status is 200': (checkedResponse) => checkedResponse.status === 200,
  });

  latitude += LATITUDE_STEP_DEGREES;

  sleep(1); // stand-in for "time between polls"
}
