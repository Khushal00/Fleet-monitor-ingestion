import http from 'k6/http';
import { check, sleep } from 'k6';
import { Counter, Rate } from 'k6/metrics';

const accepted    = new Counter('accepted_202');
const errorRate   = new Rate('error_rate');

// These vehicles will repeatedly send alert-triggering telemetry
// Only 1 alert per vehicle per alert-type should be written to DB
const ALERT_VEHICLES = [
  { id: 'VH_DEDUP_SPEED_01', fleet: 'fleet_delhi_jaipur', key: 'fleet_delhi_jaipur_key',
    state: { speed_kmh: 130, fuel_pct: 60, engine_temp_celsius: 85 } },
  { id: 'VH_DEDUP_FUEL_01',  fleet: 'fleet_mumbai_pune',  key: 'fleet_mumbai_pune_key',
    state: { speed_kmh: 55,  fuel_pct: 6,  engine_temp_celsius: 85 } },
  { id: 'VH_DEDUP_HEAT_01',  fleet: 'fleet_bangalore',    key: 'fleet_bangalore_key',
    state: { speed_kmh: 45,  fuel_pct: 55, engine_temp_celsius: 112 } },
  { id: 'VH_DEDUP_ALL_01',   fleet: 'test_fleet',         key: 'test_key',
    state: { speed_kmh: 135, fuel_pct: 4,  engine_temp_celsius: 115 } },
];

export const options = {
  scenarios: {
    alert_storm: {
      executor:   'constant-vus',
      vus:        20,
      duration:   '60s',
    },
  },
  thresholds: {
    error_rate: ['rate<0.01'],
  },
};

export default function () {
  const vehicle = ALERT_VEHICLES[Math.floor(Math.random() * ALERT_VEHICLES.length)];

  const payload = JSON.stringify({
    vehicle_id: vehicle.id,
    fleet_id:   vehicle.fleet,
    timestamp:  new Date().toISOString(),
    location:   { latitude: 28.7041, longitude: 77.1025 },
    vehicle_state: {
      ...vehicle.state,
      battery_voltage: 12.6,
      odometer_km:     15000,
      is_moving:       true,
      engine_on:       true,
    },
  });

  const res = http.post(
    'http://localhost:8001/api/v1/telemetry',
    payload,
    { headers: { 'Content-Type': 'application/json', 'X-API-Key': vehicle.key } }
  );

  const ok = check(res, { 'status is 202': (r) => r.status === 202 });
  errorRate.add(!ok);
  if (ok) accepted.add(1);

  sleep(0.1);
}

export function handleSummary(data) {
  return {
    'results/alert_dedup.json': JSON.stringify(data, null, 2),
  };
}