package jobs

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"

	"fleet-monitor/serving/internal/config"
	"fleet-monitor/serving/internal/store"
	"fleet-monitor/serving/internal/ws"
)

func TestJobTicksIntegration(t *testing.T) {
	if os.Getenv("RUN_INTEGRATION_TESTS") != "1" {
		t.Skip("set RUN_INTEGRATION_TESTS=1 to run against local Redis and TimescaleDB")
	}
	_ = godotenv.Load("../../.env")
	ctx := context.Background()
	cfg := config.Load()
	dbStore, err := store.NewTimescaleStore(ctx, cfg)
	if err != nil {
		t.Fatalf("connect TimescaleDB: %v", err)
	}
	defer dbStore.Close()
	redisStore, err := store.NewRedisStore(ctx, cfg)
	if err != nil {
		t.Fatalf("connect Redis: %v", err)
	}
	defer redisStore.Close()

	t.Run("heartbeat transitions offline then online", func(t *testing.T) {
		f := newJobFixture(t, ctx, dbStore.Pool(), redisStore.Client())
		defer f.cleanup(t)
		h := NewHeartbeatMonitor(redisStore.Client(), dbStore.Pool(), ws.NewHub(nil, nil), 1, 60)
		h.tick(ctx)
		if got := redisStore.Client().Get(ctx, f.onlineKey()).Val(); got != "offline" {
			t.Fatalf("status without state = %q, want offline", got)
		}
		redisStore.Client().HSet(ctx, f.stateKey(), "received_at", time.Now().Unix())
		h.tick(ctx)
		if got := redisStore.Client().Get(ctx, f.onlineKey()).Val(); got != "online" {
			t.Fatalf("fresh state status = %q, want online", got)
		}
	})

	t.Run("deviation sets state, persists alert, and resolves on route", func(t *testing.T) {
		f := newJobFixture(t, ctx, dbStore.Pool(), redisStore.Client())
		defer f.cleanup(t)
		redisStore.Client().HSet(ctx, f.stateKey(), "lat", 1.0, "lng", 1.0)
		d := NewDeviationDetector(redisStore.Client(), dbStore.Pool(), ws.NewHub(nil, nil), 1)
		d.tick(ctx)
		if got := redisStore.Client().Get(ctx, f.deviationKey()).Val(); got != "true" {
			t.Fatalf("far-away vehicle deviation = %q, want true", got)
		}
		var open int
		if err := dbStore.Pool().QueryRow(ctx, "SELECT COUNT(*) FROM vehicle_alerts WHERE vehicle_id=$1 AND alert_type='ROUTE_DEVIATION' AND resolved_at IS NULL", f.vehicleID).Scan(&open); err != nil || open != 1 {
			t.Fatalf("open deviation alerts = %d, err=%v", open, err)
		}
		redisStore.Client().HSet(ctx, f.stateKey(), "lat", 0.0, "lng", 0.0)
		d.tick(ctx)
		if got := redisStore.Client().Get(ctx, f.deviationKey()).Val(); got != "false" {
			t.Fatalf("on-route vehicle deviation = %q, want false", got)
		}
		if err := dbStore.Pool().QueryRow(ctx, "SELECT COUNT(*) FROM vehicle_alerts WHERE vehicle_id=$1 AND alert_type='ROUTE_DEVIATION' AND resolved_at IS NOT NULL", f.vehicleID).Scan(&open); err != nil || open != 1 {
			t.Fatalf("resolved deviation alerts = %d, err=%v", open, err)
		}
	})

	t.Run("stop arrival completes final-stop trip", func(t *testing.T) {
		f := newJobFixture(t, ctx, dbStore.Pool(), redisStore.Client())
		defer f.cleanup(t)
		redisStore.Client().HSet(ctx, f.stateKey(), "lat", 0.0, "lng", 0.0)
		s := NewStopDetector(redisStore.Client(), dbStore.Pool(), ws.NewHub(nil, nil), 1)
		s.tick(ctx)
		var status string
		if err := dbStore.Pool().QueryRow(ctx, "SELECT status FROM trip_stop_progress WHERE trip_id=$1 AND stop_id=$2", f.tripID, f.stopID).Scan(&status); err != nil || status != "ARRIVED" {
			t.Fatalf("stop status = %q, err=%v", status, err)
		}
		if err := dbStore.Pool().QueryRow(ctx, "SELECT status FROM trip WHERE trip_id=$1", f.tripID).Scan(&status); err != nil || status != "COMPLETED" {
			t.Fatalf("trip status = %q, err=%v", status, err)
		}
	})

	t.Run("eta stores a future value for the next pending stop", func(t *testing.T) {
		f := newJobFixture(t, ctx, dbStore.Pool(), redisStore.Client())
		defer f.cleanup(t)
		redisStore.Client().HSet(ctx, f.stateKey(), "lat", 0.0, "lng", -1.0, "speed_kmh", 60.0)
		e := NewETAEstimator(redisStore.Client(), dbStore.Pool(), 1)
		e.tick(ctx)
		value, err := redisStore.Client().Get(ctx, f.etaKey()).Result()
		if err != nil {
			t.Fatalf("read ETA: %v", err)
		}
		eta, err := time.Parse(time.RFC3339, value)
		if err != nil || !eta.After(time.Now().UTC()) {
			t.Fatalf("ETA = %q (%v), want future RFC3339", value, err)
		}
	})
}

type jobFixture struct {
	ctx                                   context.Context
	db                                    *pgxpool.Pool
	redis                                 *redis.Client
	vehicleID, fleetID, routeID, driverID string
	tripID, stopID                        string
}

func newJobFixture(t *testing.T, ctx context.Context, db *pgxpool.Pool, redisClient *redis.Client) jobFixture {
	t.Helper()
	suffix := fmt.Sprintf("job_tick_%d", time.Now().UnixNano())
	f := jobFixture{ctx: ctx, db: db, redis: redisClient, vehicleID: "v_" + suffix, fleetID: "f_" + suffix, routeID: "r_" + suffix, driverID: "d_" + suffix, tripID: "t_" + suffix, stopID: "s_" + suffix}
	for _, query := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO vehicle_registry (vehicle_id, fleet_id, display_name, registration_number, vehicle_type) VALUES ($1,$2,'Test vehicle','TEST-1','truck')", []any{f.vehicleID, f.fleetID}},
		{"INSERT INTO driver_registry (driver_id, full_name, phone_number, license_number, license_expiry) VALUES ($1,'Test driver','000','LIC',CURRENT_DATE + 1)", []any{f.driverID}},
		{"INSERT INTO route_registry (route_id, route_name, origin_name, origin_lat, origin_lng, destination_name, destination_lat, destination_lng, corridor_radius_km) VALUES ($1,'Test route','Origin',0,0,'Destination',0,1,0.1)", []any{f.routeID}},
		{"INSERT INTO route_stops (stop_id, route_id, stop_sequence, stop_name, lat, lng, arrival_radius_km) VALUES ($1,$2,1,'Final stop',0,0,0.2)", []any{f.stopID, f.routeID}},
		{"INSERT INTO fleet_config (fleet_id, staleness_threshold_seconds) VALUES ($1,60)", []any{f.fleetID}},
		{"INSERT INTO trip (trip_id, vehicle_id, driver_id, route_id, status, scheduled_departure) VALUES ($1,$2,$3,$4,'IN_PROGRESS',NOW())", []any{f.tripID, f.vehicleID, f.driverID, f.routeID}},
		{"INSERT INTO trip_stop_progress (trip_id, stop_id, status) VALUES ($1,$2,'PENDING')", []any{f.tripID, f.stopID}},
	} {
		if _, err := db.Exec(ctx, query.sql, query.args...); err != nil {
			t.Fatalf("seed fixture: %v", err)
		}
	}
	return f
}

func (f jobFixture) stateKey() string     { return fmt.Sprintf("vehicle:%s:state", f.vehicleID) }
func (f jobFixture) onlineKey() string    { return fmt.Sprintf("vehicle:%s:online_status", f.vehicleID) }
func (f jobFixture) deviationKey() string { return fmt.Sprintf("vehicle:%s:deviation", f.vehicleID) }
func (f jobFixture) etaKey() string       { return fmt.Sprintf("trip:%s:eta", f.tripID) }

func (f jobFixture) cleanup(t *testing.T) {
	t.Helper()
	_, _ = f.db.Exec(f.ctx, "DELETE FROM trip_stop_progress WHERE trip_id=$1", f.tripID)
	_, _ = f.db.Exec(f.ctx, "DELETE FROM trip WHERE trip_id=$1", f.tripID)
	_, _ = f.db.Exec(f.ctx, "DELETE FROM route_stops WHERE route_id=$1", f.routeID)
	_, _ = f.db.Exec(f.ctx, "DELETE FROM route_registry WHERE route_id=$1", f.routeID)
	_, _ = f.db.Exec(f.ctx, "DELETE FROM driver_registry WHERE driver_id=$1", f.driverID)
	_, _ = f.db.Exec(f.ctx, "DELETE FROM vehicle_registry WHERE vehicle_id=$1", f.vehicleID)
	_, _ = f.db.Exec(f.ctx, "DELETE FROM fleet_config WHERE fleet_id=$1", f.fleetID)
	_, _ = f.db.Exec(f.ctx, "DELETE FROM vehicle_alerts WHERE vehicle_id=$1", f.vehicleID)
	_ = f.redis.Del(f.ctx, f.stateKey(), f.onlineKey(), f.deviationKey(), f.etaKey(), fmt.Sprintf("deviation:%s:%s", f.vehicleID, f.tripID)).Err()
}
