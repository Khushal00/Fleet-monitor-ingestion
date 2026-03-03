import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Trend, Counter } from 'k6/metrics';

const errorRate        = new Rate('error_rate');
const reqDuration      = new Trend('req_duration', true);
const preSpikeDuration = new Trend('pre_spike_latency',  true);
const spikeDuration    = new Trend('spike_latency',      true);
const postSpikeDuration= new Trend('post_spike_latency', true);

const FLEETS = [
  { key: 'fleet_delhi_jaipur_key', fleet: 'fleet_delhi_jaipur', lat: 28.7041, lng: 77.1025 },
  { key: 'fleet_mumbai_pune_key',  fleet: 'fleet_mumbai_pune',  lat: 19.0760, lng: 72.8777 },
  { key: 'fleet_bangalore_key',    fleet: 'fleet_bangalore',    lat: 12.9716, lng: 77.5946 },
  { key: 'test_key',               fleet: 'test_fleet',         lat: 28.7041, lng: 77.1025 },
];

// Spike test: simulates entire fleet reconnecting simultaneously after downtime
// Real-world scenario: GPS devices reboot after power cut, all reconnect at once
// Goal: prove goroutine worker pool + channel buffers absorb burst without drops
// Key metric: channel_drops must stay 0, system must recover to baseline latency after spike
export const options = {
  stages: [
    { duration: '30s', target: 50   }, // baseline — normal fleet traffic
    { duration: '10s', target: 50   }, // hold baseline
    { duration: '10s', target: 1500 }, // SPIKE — entire fleet reconnects instantly
    { duration: '30s', target: 1500 }, // hold spike
    { duration: '10s', target: 50   }, // drop back to baseline
    { duration: '30s', target: 50   }, // recovery — watch latency normalise
    { duration: '10s', target: 0    },
  ],
  thresholds: {
    http_req_duration:   ['p(95)<3000'],
    error_rate:          ['rate<0.05'],
    'spike_latency':     ['p(99)<5000'],
    'post_spike_latency':['p(95)<500'],  // must recover to near-baseline after spike
  },
};

// track test phase via elapsed time
const TEST_START = Date.now();

function getPhase() {
  const elapsed = (Date.now() - TEST_START) / 1000;
  if (elapsed < 40)  return 'pre_spike';
  if (elapsed < 90)  return 'spike';
  return 'post_spike';
}

export default function () {
  const fleet     = FLEETS[Math.floor(Math.random() * FLEETS.length)];
  const vehicleId = `VH_SPIKE_${Math.floor(Math.random() * 1500)}`;

  const payload = JSON.stringify({
    vehicle_id: vehicleId,
    fleet_id:   fleet.fleet,
    timestamp:  new Date().toISOString(),
    location: {
      latitude:  fleet.lat + (Math.random() - 0.5) * 0.2,
      longitude: fleet.lng + (Math.random() - 0.5) * 0.2,
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
      timeout: '15s',
    }
  );

  const ok = check(res, { 'status is 202': (r) => r.status === 202 });
  errorRate.add(!ok);
  reqDuration.add(res.timings.duration);

  const phase = getPhase();
  if (phase === 'pre_spike')  preSpikeDuration.add(res.timings.duration);
  if (phase === 'spike')      spikeDuration.add(res.timings.duration);
  if (phase === 'post_spike') postSpikeDuration.add(res.timings.duration);

  sleep(0.01);
}

export function handleSummary(data) {
  return {
    'results/spike_test.json': JSON.stringify(data, null, 2),
  };
}
