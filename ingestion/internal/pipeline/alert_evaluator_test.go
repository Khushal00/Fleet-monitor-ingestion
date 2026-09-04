package pipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/joho/godotenv"

	"fleet-monitor/ingestion/internal/config"
	"fleet-monitor/ingestion/internal/domain"
	"fleet-monitor/ingestion/internal/store"
)

type failingAlertStore struct{ err error }

func (s failingAlertStore) InsertAlert(context.Context, string, string, domain.AlertType, domain.AlertSeverity, float64) error {
	return s.err
}

type memoryAlertDedupStore struct {
	mu       sync.Mutex
	claims   map[string]bool
	releases int
}

func (s *memoryAlertDedupStore) TryClaimAlertDedup(_ context.Context, vehicleID string, alertType domain.AlertType) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := fmt.Sprintf("%s:%s", vehicleID, alertType)
	if s.claims[key] {
		return false, nil
	}
	s.claims[key] = true
	return true, nil
}

func (s *memoryAlertDedupStore) ReleaseAlertDedup(_ context.Context, vehicleID string, alertType domain.AlertType) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.claims, fmt.Sprintf("%s:%s", vehicleID, alertType))
	s.releases++
	return nil
}

func (s *memoryAlertDedupStore) PublishAlert(context.Context, string, []byte) error { return nil }

func TestEvaluateReleasesClaimWhenInsertFails(t *testing.T) {
	redis := &memoryAlertDedupStore{claims: make(map[string]bool)}
	evaluator := &AlertEvaluator{
		db:    failingAlertStore{err: errors.New("temporary database outage")},
		redis: redis,
		rules: domain.DefaultAlertRules,
	}
	msg := &domain.TelemetryMessage{
		VehicleID:   "VH_RELEASE_RETRY",
		FleetID:     "test_fleet",
		SpeedKmh:    130,
		FuelPct:     50,
		EngineTempC: 80,
		Timestamp:   time.Now().UTC(),
	}

	evaluator.evaluate(context.Background(), msg)

	if redis.releases != 1 {
		t.Fatalf("released claims = %d, want 1", redis.releases)
	}
	claimed, err := redis.TryClaimAlertDedup(context.Background(), msg.VehicleID, domain.AlertSpeeding)
	if err != nil {
		t.Fatalf("retry claim: %v", err)
	}
	if !claimed {
		t.Fatal("retry claim was not released after insert failure")
	}
}

func TestEvaluateReleasesRedisClaimWhenInsertFailsIntegration(t *testing.T) {
	if os.Getenv("RUN_INTEGRATION_TESTS") != "1" {
		t.Skip("set RUN_INTEGRATION_TESTS=1 to run against local Redis")
	}

	_ = godotenv.Load("../../.env")
	ctx := context.Background()
	redisStore, err := store.NewRedisStore(ctx, config.Load())
	if err != nil {
		t.Fatalf("connect Redis: %v", err)
	}
	defer redisStore.Close()

	vehicleID := fmt.Sprintf("VH_RELEASE_RETRY_%d", time.Now().UnixNano())
	defer redisStore.ReleaseAlertDedup(ctx, vehicleID, domain.AlertSpeeding)
	evaluator := &AlertEvaluator{
		db:    failingAlertStore{err: errors.New("temporary database outage")},
		redis: redisStore,
		rules: domain.DefaultAlertRules,
	}
	evaluator.evaluate(ctx, &domain.TelemetryMessage{
		VehicleID: vehicleID, FleetID: "test_fleet", SpeedKmh: 130, FuelPct: 50, EngineTempC: 80,
	})

	claimed, err := redisStore.TryClaimAlertDedup(ctx, vehicleID, domain.AlertSpeeding)
	if err != nil {
		t.Fatalf("retry claim: %v", err)
	}
	if !claimed {
		t.Fatal("Redis dedup key remained after the failed insert")
	}
}
