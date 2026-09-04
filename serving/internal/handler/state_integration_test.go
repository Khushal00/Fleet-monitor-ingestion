package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"

	"fleet-monitor/serving/internal/config"
	"fleet-monitor/serving/internal/store"
)

func TestHandlerStateAndErrorBranchesIntegration(t *testing.T) {
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
	f := newHandlerFixture(t, ctx, dbStore.Pool(), redisStore.Client())
	defer f.cleanup(t)

	alerts := NewAlertHandler(redisStore.Client(), dbStore.Pool())
	vehicles := NewVehicleHandler(redisStore.Client(), dbStore.Pool())
	trips := NewTripHandler(redisStore.Client(), dbStore.Pool())
	analytics := NewAnalyticsHandler(redisStore.Client(), dbStore.Pool())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /alerts", alerts.HandleAttentionQueue)
	mux.HandleFunc("GET /alerts/{alert_id}", alerts.HandleAlertDetail)
	mux.HandleFunc("POST /alerts/{alert_id}/acknowledge", alerts.HandleAcknowledge)
	mux.HandleFunc("POST /alerts/{alert_id}/unacknowledge", alerts.HandleUnacknowledge)
	mux.HandleFunc("POST /alerts/{alert_id}/resolve", alerts.HandleResolve)
	mux.HandleFunc("GET /vehicles/{vehicle_id}/panel", vehicles.HandlePanel)
	mux.HandleFunc("GET /vehicles/{vehicle_id}/active-trip", vehicles.HandleActiveTrip)
	mux.HandleFunc("GET /vehicles/{vehicle_id}/alerts", vehicles.HandleVehicleAlerts)
	mux.HandleFunc("GET /trips", trips.HandleList)
	mux.HandleFunc("GET /trips/{trip_id}", trips.HandleDetail)
	mux.HandleFunc("GET /analytics/summary", analytics.HandleSummary)

	assertHandlerStatus(t, mux, http.MethodGet, "/alerts?fleet_id="+f.fleetID, "", http.StatusOK)
	assertHandlerStatus(t, mux, http.MethodGet, "/alerts/"+fmt.Sprint(f.alertID), "", http.StatusOK)
	assertHandlerStatus(t, mux, http.MethodPost, "/alerts/"+fmt.Sprint(f.alertID)+"/acknowledge", `{"operator":"ops"}`, http.StatusOK)
	var acknowledged bool
	if err := dbStore.Pool().QueryRow(ctx, "SELECT acknowledged_at IS NOT NULL FROM vehicle_alerts WHERE id=$1", f.alertID).Scan(&acknowledged); err != nil || !acknowledged {
		t.Fatalf("acknowledged = %v, err=%v", acknowledged, err)
	}
	assertHandlerStatus(t, mux, http.MethodPost, "/alerts/"+fmt.Sprint(f.alertID)+"/unacknowledge", "", http.StatusOK)
	assertHandlerStatus(t, mux, http.MethodPost, "/alerts/"+fmt.Sprint(f.alertID)+"/resolve", `{"operator":"ops"}`, http.StatusOK)
	var resolved bool
	if err := dbStore.Pool().QueryRow(ctx, "SELECT resolved_at IS NOT NULL FROM vehicle_alerts WHERE id=$1", f.alertID).Scan(&resolved); err != nil || !resolved {
		t.Fatalf("resolved = %v, err=%v", resolved, err)
	}
	assertHandlerStatus(t, mux, http.MethodPost, "/alerts/999999999/acknowledge", `{"operator":"ops"}`, http.StatusNotFound)

	assertHandlerStatus(t, mux, http.MethodGet, "/vehicles/"+f.vehicleID+"/panel", "", http.StatusOK)
	assertHandlerStatus(t, mux, http.MethodGet, "/vehicles/"+f.vehicleID+"/active-trip", "", http.StatusOK)
	assertHandlerStatus(t, mux, http.MethodGet, "/vehicles/"+f.vehicleID+"/alerts", "", http.StatusOK)
	assertHandlerStatus(t, mux, http.MethodGet, "/trips?fleet_id="+f.fleetID+"&status=IN_PROGRESS", "", http.StatusOK)
	assertHandlerStatus(t, mux, http.MethodGet, "/trips/"+f.tripID, "", http.StatusOK)
	assertHandlerStatus(t, mux, http.MethodGet, "/analytics/summary?fleet_id="+f.fleetID, "", http.StatusOK)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/analytics/summary", nil)
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing analytics fleet status = %d, want 400", w.Code)
	}
}

func assertHandlerStatus(t *testing.T, h http.Handler, method, target, body string, want int) {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	h.ServeHTTP(w, req)
	if w.Code != want {
		t.Fatalf("%s %s status = %d, want %d; body=%s", method, target, w.Code, want, w.Body.String())
	}
	if !json.Valid(w.Body.Bytes()) {
		t.Fatalf("%s %s returned invalid JSON: %s", method, target, w.Body.String())
	}
}

type handlerFixture struct {
	ctx                                                   context.Context
	db                                                    *pgxpool.Pool
	redis                                                 *redis.Client
	vehicleID, fleetID, routeID, driverID, tripID, stopID string
	alertID                                               int64
}

func newHandlerFixture(t *testing.T, ctx context.Context, db *pgxpool.Pool, redisClient *redis.Client) handlerFixture {
	t.Helper()
	suffix := fmt.Sprintf("handler_%d", time.Now().UnixNano())
	f := handlerFixture{ctx: ctx, db: db, redis: redisClient, vehicleID: "v_" + suffix, fleetID: "f_" + suffix, routeID: "r_" + suffix, driverID: "d_" + suffix, tripID: "t_" + suffix, stopID: "s_" + suffix}
	queries := []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO vehicle_registry (vehicle_id, fleet_id, display_name, registration_number, vehicle_type) VALUES ($1,$2,'Handler vehicle','HANDLER-1','truck')", []any{f.vehicleID, f.fleetID}},
		{"INSERT INTO driver_registry (driver_id, full_name, phone_number, license_number, license_expiry) VALUES ($1,'Handler driver','000','LIC',CURRENT_DATE + 1)", []any{f.driverID}},
		{"INSERT INTO route_registry (route_id, route_name, origin_name, origin_lat, origin_lng, destination_name, destination_lat, destination_lng, corridor_radius_km, total_distance_km) VALUES ($1,'Handler route','Origin',0,0,'Destination',0,1,1,100)", []any{f.routeID}},
		{"INSERT INTO route_stops (stop_id, route_id, stop_sequence, stop_name, lat, lng, arrival_radius_km) VALUES ($1,$2,1,'Stop',0,1,1)", []any{f.stopID, f.routeID}},
		{"INSERT INTO fleet_config (fleet_id, staleness_threshold_seconds) VALUES ($1,60)", []any{f.fleetID}},
		{"INSERT INTO trip (trip_id, vehicle_id, driver_id, route_id, status, scheduled_departure) VALUES ($1,$2,$3,$4,'IN_PROGRESS',NOW())", []any{f.tripID, f.vehicleID, f.driverID, f.routeID}},
		{"INSERT INTO trip_stop_progress (trip_id, stop_id, status) VALUES ($1,$2,'PENDING')", []any{f.tripID, f.stopID}},
	}
	for _, q := range queries {
		if _, err := db.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatalf("seed handler fixture: %v", err)
		}
	}
	if err := db.QueryRow(ctx, "INSERT INTO vehicle_alerts (vehicle_id, fleet_id, alert_type, severity, triggered_value) VALUES ($1,$2,'SPEEDING','WARNING',120) RETURNING id", f.vehicleID, f.fleetID).Scan(&f.alertID); err != nil {
		t.Fatalf("seed alert: %v", err)
	}
	if err := redisClient.HSet(ctx, fmt.Sprintf("vehicle:%s:state", f.vehicleID), map[string]any{"fleet_id": f.fleetID, "speed_kmh": 120, "fuel_pct": 50, "engine_temp": 90, "battery": 12.5, "is_moving": true, "engine_on": true, "received_at": time.Now().Unix()}).Err(); err != nil {
		t.Fatalf("seed redis state: %v", err)
	}
	if err := redisClient.Set(ctx, fmt.Sprintf("vehicle:%s:online_status", f.vehicleID), "online", 0).Err(); err != nil {
		t.Fatalf("seed online state: %v", err)
	}
	if err := redisClient.Set(ctx, fmt.Sprintf("trip:%s:eta", f.tripID), time.Now().Add(time.Hour).UTC().Format(time.RFC3339), 0).Err(); err != nil {
		t.Fatalf("seed ETA: %v", err)
	}
	return f
}

func (f handlerFixture) cleanup(t *testing.T) {
	t.Helper()
	_, _ = f.db.Exec(f.ctx, "DELETE FROM vehicle_alerts WHERE vehicle_id=$1", f.vehicleID)
	_, _ = f.db.Exec(f.ctx, "DELETE FROM trip_stop_progress WHERE trip_id=$1", f.tripID)
	_, _ = f.db.Exec(f.ctx, "DELETE FROM trip WHERE trip_id=$1", f.tripID)
	_, _ = f.db.Exec(f.ctx, "DELETE FROM route_stops WHERE route_id=$1", f.routeID)
	_, _ = f.db.Exec(f.ctx, "DELETE FROM route_registry WHERE route_id=$1", f.routeID)
	_, _ = f.db.Exec(f.ctx, "DELETE FROM driver_registry WHERE driver_id=$1", f.driverID)
	_, _ = f.db.Exec(f.ctx, "DELETE FROM vehicle_registry WHERE vehicle_id=$1", f.vehicleID)
	_, _ = f.db.Exec(f.ctx, "DELETE FROM fleet_config WHERE fleet_id=$1", f.fleetID)
	_ = f.redis.Del(f.ctx, fmt.Sprintf("vehicle:%s:state", f.vehicleID), fmt.Sprintf("vehicle:%s:online_status", f.vehicleID), fmt.Sprintf("trip:%s:eta", f.tripID)).Err()
}
