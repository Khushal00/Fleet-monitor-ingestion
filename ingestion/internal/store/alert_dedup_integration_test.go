package store

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/joho/godotenv"

	"fleet-monitor/ingestion/internal/config"
	"fleet-monitor/ingestion/internal/domain"
)

const integrationTestsEnv = "RUN_INTEGRATION_TESTS"

func TestAlertDedupConcurrentClaimsIntegration(t *testing.T) {
	if os.Getenv(integrationTestsEnv) != "1" {
		t.Skip("set RUN_INTEGRATION_TESTS=1 to run against local Redis and TimescaleDB")
	}

	_ = godotenv.Load("../../.env")
	ctx := context.Background()
	cfg := config.Load()
	db, err := NewTimescaleStore(ctx, cfg)
	if err != nil {
		t.Fatalf("connect TimescaleDB: %v", err)
	}
	defer db.Close()
	redisStore, err := NewRedisStore(ctx, cfg)
	if err != nil {
		t.Fatalf("connect Redis: %v", err)
	}
	defer redisStore.Close()

	vehicleID := fmt.Sprintf("TEST_ALERT_DEDUP_%d", time.Now().UnixNano())
	alertType := domain.AlertSpeeding
	key := fmt.Sprintf("alert:%s:%s", vehicleID, alertType)
	conn := integrationConn(t, ctx, cfg)
	defer conn.Close(ctx)
	defer func() {
		_, _ = conn.Exec(ctx, "DELETE FROM vehicle_alerts WHERE vehicle_id = $1", vehicleID)
		_ = redisStore.Client().Del(ctx, key).Err()
	}()

	const workers = 10
	start := make(chan struct{})
	var claims atomic.Int64
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			claimed, err := redisStore.TryClaimAlertDedup(ctx, vehicleID, alertType)
			if err != nil {
				t.Errorf("claim alert dedup: %v", err)
				return
			}
			if !claimed {
				return
			}
			claims.Add(1)
			if err := db.InsertAlert(ctx, vehicleID, "test_fleet", alertType, domain.SeverityWarning, 130); err != nil {
				t.Errorf("insert claimed alert: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := claims.Load(); got != 1 {
		t.Errorf("successful claims = %d, want 1", got)
	}
	if got := alertRowCount(t, ctx, conn, vehicleID, alertType); got != 1 {
		t.Errorf("persisted alert rows = %d, want 1", got)
	}
}

func TestAlertDedupDatabaseBackstopIntegration(t *testing.T) {
	if os.Getenv(integrationTestsEnv) != "1" {
		t.Skip("set RUN_INTEGRATION_TESTS=1 to run against local Redis and TimescaleDB")
	}

	_ = godotenv.Load("../../.env")
	ctx := context.Background()
	cfg := config.Load()
	db, err := NewTimescaleStore(ctx, cfg)
	if err != nil {
		t.Fatalf("connect TimescaleDB: %v", err)
	}
	defer db.Close()
	conn := integrationConn(t, ctx, cfg)
	defer conn.Close(ctx)

	vehicleID := fmt.Sprintf("TEST_ALERT_DB_BACKSTOP_%d", time.Now().UnixNano())
	alertType := domain.AlertSpeeding
	defer func() { _, _ = conn.Exec(ctx, "DELETE FROM vehicle_alerts WHERE vehicle_id = $1", vehicleID) }()

	const workers = 10
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := db.InsertAlert(ctx, vehicleID, "test_fleet", alertType, domain.SeverityWarning, 130); err != nil {
				t.Errorf("insert alert: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := alertRowCount(t, ctx, conn, vehicleID, alertType); got != 1 {
		t.Errorf("database-backstop rows = %d, want 1", got)
	}
}

func integrationConn(t *testing.T, ctx context.Context, cfg *config.Config) *pgx.Conn {
	t.Helper()
	conn, err := pgx.Connect(ctx, fmt.Sprintf("postgres://%s:%s@%s:%s/%s", cfg.DBUser, cfg.DBPassword, cfg.DBHost, cfg.DBPort, cfg.DBName))
	if err != nil {
		t.Fatalf("connect verification database: %v", err)
	}
	return conn
}

func alertRowCount(t *testing.T, ctx context.Context, conn *pgx.Conn, vehicleID string, alertType domain.AlertType) int {
	t.Helper()
	var count int
	if err := conn.QueryRow(ctx, "SELECT COUNT(*) FROM vehicle_alerts WHERE vehicle_id = $1 AND alert_type = $2", vehicleID, string(alertType)).Scan(&count); err != nil {
		t.Fatalf("count alert rows: %v", err)
	}
	return count
}
