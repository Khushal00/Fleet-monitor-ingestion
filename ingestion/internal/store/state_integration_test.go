package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/joho/godotenv"

	"fleet-monitor/ingestion/internal/config"
	"fleet-monitor/ingestion/internal/domain"
)

func TestPipelineStateUpdatesIntegration(t *testing.T) {
	if os.Getenv(integrationTestsEnv) != "1" {
		t.Skip("set RUN_INTEGRATION_TESTS=1 to run against local Redis")
	}
	_ = godotenv.Load("../../.env")
	ctx := context.Background()
	redisStore, err := NewRedisStore(ctx, config.Load())
	if err != nil {
		t.Fatalf("connect Redis: %v", err)
	}
	defer redisStore.Close()

	vehicleID := fmt.Sprintf("TEST_STATE_%d", time.Now().UnixNano())
	fleetID := "test_fleet"
	stateKey := fmt.Sprintf("vehicle:%s:state", vehicleID)
	channel := fmt.Sprintf("fleet:%s:telemetry", fleetID)
	defer redisStore.Client().Del(ctx, stateKey)

	sub := redisStore.Client().Subscribe(ctx, channel)
	defer sub.Close()
	if _, err := sub.Receive(ctx); err != nil {
		t.Fatalf("subscribe telemetry channel: %v", err)
	}
	msg := &domain.TelemetryMessage{
		VehicleID: vehicleID, FleetID: fleetID, Latitude: 12.9716, Longitude: 77.5946,
		SpeedKmh: 42.5, FuelPct: 80, EngineTempC: 90, BatteryVoltage: 12.4,
		IsMoving: true, EngineOn: true, Timestamp: time.Unix(1_700_000_000, 0), ReceivedAt: time.Unix(1_700_000_001, 0),
	}
	if err := redisStore.PipelineStateUpdates(ctx, []*domain.TelemetryMessage{msg}); err != nil {
		t.Fatalf("pipeline state update: %v", err)
	}
	state, err := redisStore.Client().HGetAll(ctx, stateKey).Result()
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if state["vehicle_id"] != vehicleID || state["fleet_id"] != fleetID || state["speed_kmh"] != "42.5" || state["is_moving"] != "1" {
		t.Fatalf("unexpected state: %#v", state)
	}
	if ttl := redisStore.Client().TTL(ctx, stateKey).Val(); ttl <= 0 || ttl > 30*time.Second {
		t.Fatalf("state TTL = %s, want (0,30s]", ttl)
	}
	select {
	case received := <-sub.Channel():
		if received.Payload == "" {
			t.Fatal("published telemetry payload was empty")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive telemetry publication")
	}
}
