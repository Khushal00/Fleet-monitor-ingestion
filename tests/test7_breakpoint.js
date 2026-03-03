import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Trend, Counter } from 'k6/metrics';

const errorRate   = new Rate('error_rate');
const reqDuration = new Trend('req_duration', true);
const accepted    = new Counter('accepted_202');

const FLEETS = [
  { key: 'fleet_delhi_jaipur_key', fleet: 'fleet_delhi_jaipur', lat: 28.7041, lng: 77.1025 },
  { key: 'fleet_mumbai_pune_key',  fleet: 'fleet_mumbai_pune',  lat: 19.0760, lng: 72.8777 },
  { key: 'fleet_bangalore_key',    fleet: 'fleet_bangalore',    lat: 12.9716, lng: 77.5946 },
  { key: 'test_key',               fleet: 'test_fleet',         lat: 28.7041, lng: 77.1025 },
];

// Breakpoint: ramp VUs until p99 > 1s or error rate spikes
// Goal: find the exact req/sec number where the system starts degrading
// This gives your resume metric: "sustains X msg/sec before p99 exceeds 1s"
// Watch /metrics during this test — channel drops = you have found your ceiling
export const options = {
  executor: 'ramping-arrival-rate',
  stages: [
    { duration: '2m',  target: 100  },
    { duration: '2m',  target: 300  },
    { duration: '2m',  target: 600  },
    { duration: '2m',  target: 1000 },
    { duration: '2m',  target: 1500 },
    { duration: '2m',  target: 2000 },
    { duration: '2m',  target: 3000 },
  ],
  preAllocatedVUs: 200,
  maxVUs:          3000,
  thresholds: {
    // test continues past breach — we want to observe, not abort
    http_req_duration: ['p(99)<5000'],
    error_rate:        ['rate<0.20'],
  },
};

export default function () {
  const fleet     = FLEETS[Math.floor(Math.random() * FLEETS.length)];
  const vehicleId = 'VH_BP_' + Math.floor(Math.random() * 3000);

  const payload = JSON.stringify({
    vehicle_id: vehicleId,
    fleet_id:   fleet.fleet,
    timestamp:  new Date().toISOString(),
    location: {
      latitude:  fleet.lat + (Math.random() - 0.5) * 0.1,
      longitude: fleet.lng + (Math.random() - 0.5) * 0.1,
    },
    vehicle_state: {
      speed_kmh:           40 + Math.random() * 60,
      fuel_pct:            20 + Math.random() * 70,
      engine_temp_celsius: 75 + Math.random() * 20,
      battery_voltage:     12.2 + Math.random() * 0.8,
      odometer_km:         10000 + Math.random() * 50000,
      is_moving:           true,
      engine_on:           true,
    },
  });

  const res = http.post(
    'http://localhost:8001/api/v1/telemetry',
    payload,
    { headers: { 'Content-Type': 'application/json', 'X-API-Key': fleet.key }, timeout: '10s' }
  );

  const ok = check(res, { 'status is 202': (r) => r.status === 202 });
  errorRate.add(!ok);
  reqDuration.add(res.timings.duration);
  if (ok) accepted.add(1);

  sleep(0.001);
}

export function handleSummary(data) {
  return { 'results/breakpoint_test.json': JSON.stringify(data, null, 2) };
}