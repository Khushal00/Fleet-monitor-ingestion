import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Trend, Counter } from 'k6/metrics';

const errorRate   = new Rate('error_rate');
const reqDuration = new Trend('req_duration', true);
const dropped     = new Counter('dropped_requests');

const FLEETS = [
  { key: 'fleet_delhi_jaipur_key', fleet: 'fleet_delhi_jaipur', lat: 28.7041, lng: 77.1025 },
  { key: 'fleet_mumbai_pune_key',  fleet: 'fleet_mumbai_pune',  lat: 19.0760, lng: 72.8777 },
  { key: 'fleet_bangalore_key',    fleet: 'fleet_bangalore',    lat: 12.9716, lng: 77.5946 },
  { key: 'test_key',               fleet: 'test_fleet',         lat: 28.7041, lng: 77.1025 },
];

// Stress test: push well beyond expected capacity
// Goal: find which component breaks first
// Expected failure points: DB batch writer backpressure, Redis pipeline, channel buffer drops
export const options = {
  stages: [
    { duration: '30s', target: 100  }, // normal load baseline
    { duration: '30s', target: 500  }, // beyond normal
    { duration: '30s', target: 1000 }, // heavy stress
    { duration: '30s', target: 1500 }, // severe stress
    { duration: '30s', target: 2000 }, // breaking point zone
    { duration: '60s', target: 2000 }, // hold at breaking point
    { duration: '30s', target: 0    }, // ramp down — watch recovery
  ],
  thresholds: {
    // intentionally lenient — we want to observe degradation, not fail early
    http_req_duration: ['p(95)<2000'],
    error_rate:        ['rate<0.10'],  // allow up to 10% errors under stress
  },
};

export default function () {
  const fleet     = FLEETS[Math.floor(Math.random() * FLEETS.length)];
  const vehicleId = `VH_STRESS_${fleet.fleet.toUpperCase()}_${Math.floor(Math.random() * 500)}`;

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
    {
      headers: { 'Content-Type': 'application/json', 'X-API-Key': fleet.key },
      timeout: '10s',
    }
  );

  const ok = check(res, {
    'status is 202':     (r) => r.status === 202,
    'no 5xx errors':     (r) => r.status < 500,
    'latency under 2s':  (r) => r.timings.duration < 2000,
  });

  errorRate.add(!ok);
  reqDuration.add(res.timings.duration);
  if (res.status !== 202) dropped.add(1);

  sleep(0.005); // minimal sleep — maximize pressure
}

export function handleSummary(data) {
  return {
    'results/stress_test.json': JSON.stringify(data, null, 2),
  };
}
