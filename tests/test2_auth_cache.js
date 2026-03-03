import http from 'k6/http';
import { check, sleep } from 'k6';
import { Trend, Rate } from 'k6/metrics';

const firstHitLatency  = new Trend('first_hit_latency',  true);
const cachedLatency    = new Trend('cached_hit_latency', true);
const errorRate        = new Rate('error_rate');

export const options = {
  scenarios: {
    // Scenario 1: fresh VUs — each hits auth cold
    cold_auth: {
      executor:          'per-vu-iterations',
      vus:               50,
      iterations:        1,
      startTime:         '0s',
      gracefulStop:      '10s',
    },
    // Scenario 2: same VUs hammer repeatedly — hits in-memory cache
    warm_auth: {
      executor:          'constant-vus',
      vus:               50,
      duration:          '60s',
      startTime:         '15s',
    },
  },
  thresholds: {
    'cached_hit_latency': ['p(95)<50'],  // cached hits must be <50ms
    error_rate:           ['rate<0.01'],
  },
};

const payload = JSON.stringify({
  vehicle_id: 'VH_AUTH_TEST',
  fleet_id:   'test_fleet',
  timestamp:  new Date().toISOString(),
  location:   { latitude: 28.7041, longitude: 77.1025 },
  vehicle_state: {
    speed_kmh: 60, fuel_pct: 50, engine_temp_celsius: 85,
    battery_voltage: 12.6, odometer_km: 15000,
    is_moving: true, engine_on: true,
  },
});

const headers = {
  'Content-Type': 'application/json',
  'X-API-Key':    'test_key',
};

// track whether this VU has made its first request
const vuFirstHit = {};

export default function () {
  const isFirst = !vuFirstHit[__VU];
  vuFirstHit[__VU] = true;

  const res = http.post('http://localhost:8001/api/v1/telemetry', payload, { headers });
  const ok  = check(res, { 'status is 202': (r) => r.status === 202 });

  errorRate.add(!ok);

  if (isFirst) {
    firstHitLatency.add(res.timings.duration);
  } else {
    cachedLatency.add(res.timings.duration);
  }

  sleep(0.05);
}

export function handleSummary(data) {
  return {
    'results/auth_cache.json': JSON.stringify(data, null, 2),
  };
}