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

// Soak: moderate load for 30 min — detects memory leaks, goroutine leaks,
// Redis/DB connection pool exhaustion over sustained operation
export const options = {
  stages: [
    { duration: '2m',  target: 150 },
    { duration: '26m', target: 150 },
    { duration: '2m',  target: 0   },
  ],
  thresholds: {
    http_req_duration: ['p(95)<500', 'p(99)<1000'],
    error_rate:        ['rate<0.01'],
  },
};

export default function () {
  const fleet     = FLEETS[Math.floor(Math.random() * FLEETS.length)];
  const vehicleId = 'VH_SOAK_' + Math.floor(Math.random() * 150);

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
    { headers: { 'Content-Type': 'application/json', 'X-API-Key': fleet.key }, timeout: '5s' }
  );

  const ok = check(res, {
    'status is 202':    (r) => r.status === 202,
    'latency under 1s': (r) => r.timings.duration < 1000,
  });

  errorRate.add(!ok);
  reqDuration.add(res.timings.duration);
  if (ok) accepted.add(1);

  sleep(0.05);
}

export function handleSummary(data) {
  return { 'results/soak_test.json': JSON.stringify(data, null, 2) };
}