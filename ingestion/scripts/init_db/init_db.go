package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5"
	"github.com/joho/godotenv"
)

func main() {
	// Load .env file
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found — using system environment variables")
	}

	connStr := fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s",
		dbGetEnv("DB_USER", "fleet_user"),
		dbGetEnv("DB_PASSWORD", "fleet_password"),
		dbGetEnv("DB_HOST", "localhost"),
		dbGetEnv("DB_PORT", "5432"),
		dbGetEnv("DB_NAME", "fleet_monitor"),
	)

	ctx := context.Background()

	fmt.Println("Connecting to TimescaleDB...")
	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		log.Fatalf("Connection failed: %v\n\nMake sure TimescaleDB is running:\n  docker-compose up -d timescaledb", err)
	}
	defer conn.Close(ctx)
	fmt.Println("✓ Connected")

	// Run all steps in order
	step1_extensions(ctx, conn)
	step2_telemetry_table(ctx, conn)
	step3_alerts_table(ctx, conn)
	step4_indexes(ctx, conn)
	step5_verify(ctx, conn)

	fmt.Println("\n✅ Database initialised successfully")
	fmt.Println("   Run next: go run scripts/seed_redis.go")
}

// ─────────────────────────────────────────────────────────────
// Step 1 — Extensions
// ─────────────────────────────────────────────────────────────
func step1_extensions(ctx context.Context, conn *pgx.Conn) {
	fmt.Println("\n── Step 1: Extensions ──────────────────────────")

	// TimescaleDB — required for hypertable
	execOrFatal(ctx, conn,
		"CREATE EXTENSION IF NOT EXISTS timescaledb CASCADE;",
		"timescaledb extension",
	)

	// PostGIS — required for exact radius queries (ST_DWithin)
	execOrFatal(ctx, conn,
		"CREATE EXTENSION IF NOT EXISTS postgis;",
		"postgis extension",
	)
}

// ─────────────────────────────────────────────────────────────
// Step 2 — vehicle_telemetry table
// ─────────────────────────────────────────────────────────────
func step2_telemetry_table(ctx context.Context, conn *pgx.Conn) {
	fmt.Println("\n── Step 2: vehicle_telemetry table ─────────────")

	// Create the table
	execOrFatal(ctx, conn, `
		CREATE TABLE IF NOT EXISTS vehicle_telemetry (

			-- Time column — TimescaleDB partitions data by this
			-- TIMESTAMPTZ always stores in UTC
			timestamp            TIMESTAMPTZ      NOT NULL,

			-- Server receipt time — separate from vehicle clock
			-- Vehicle clocks drift; received_at is always accurate
			received_at          TIMESTAMPTZ      NOT NULL DEFAULT NOW(),

			-- Identity
			vehicle_id           TEXT             NOT NULL,
			fleet_id             TEXT             NOT NULL,

			-- GPS — stored as plain floats for TimescaleDB compatibility
			latitude             DOUBLE PRECISION NOT NULL,
			longitude            DOUBLE PRECISION NOT NULL,

			-- PostGIS geography column for exact radius queries
			-- GENERATED ALWAYS AS means it's auto-computed from lat/lng
			-- STORED means it's physically saved, not computed on read
			location             GEOGRAPHY(POINT, 4326)
			                     GENERATED ALWAYS AS (
			                         ST_SetSRID(
			                             ST_MakePoint(longitude, latitude),
			                             4326
			                         )::geography
			                     ) STORED,

			-- Sensor readings
			speed_kmh            DOUBLE PRECISION NOT NULL DEFAULT 0,
			fuel_pct             DOUBLE PRECISION NOT NULL DEFAULT 0,
			engine_temp_celsius  DOUBLE PRECISION NOT NULL DEFAULT 0,
			battery_voltage      DOUBLE PRECISION NOT NULL DEFAULT 0,
			odometer_km          DOUBLE PRECISION NOT NULL DEFAULT 0,

			-- Status flags
			is_moving            BOOLEAN          NOT NULL DEFAULT false,
			engine_on            BOOLEAN          NOT NULL DEFAULT false,

			-- Original JSON payload — stored for debugging and replay
			raw_payload          JSONB
		);
	`, "vehicle_telemetry table created")

	// Convert to TimescaleDB hypertable
	// This partitions data automatically into 7-day chunks
	// Queries on recent data only touch the latest chunk — very fast
	execOrFatal(ctx, conn, `
		SELECT create_hypertable(
			'vehicle_telemetry',
			'timestamp',
			if_not_exists => TRUE
		);
	`, "vehicle_telemetry converted to hypertable")
}

// ─────────────────────────────────────────────────────────────
// Step 3 — vehicle_alerts table
// ─────────────────────────────────────────────────────────────
func step3_alerts_table(ctx context.Context, conn *pgx.Conn) {
	fmt.Println("\n── Step 3: vehicle_alerts table ────────────────")

	execOrFatal(ctx, conn, `
		CREATE TABLE IF NOT EXISTS vehicle_alerts (

			-- Standard primary key — alerts are rare enough
			-- that a traditional PK is fine here
			id               BIGSERIAL        PRIMARY KEY,

			-- Identity — same values as vehicle_telemetry
			vehicle_id       TEXT             NOT NULL,
			fleet_id         TEXT             NOT NULL,

			-- Alert classification
			-- Must exactly match domain.AlertType constants:
			-- SPEEDING | LOW_FUEL | ENGINE_OVERHEAT
			alert_type       TEXT             NOT NULL,

			-- Must exactly match domain.AlertSeverity constants:
			-- INFO | WARNING | CRITICAL
			severity         TEXT             NOT NULL,

			-- The sensor value that triggered this alert
			-- e.g. speed was 127.5 km/h when SPEEDING fired
			triggered_value  DOUBLE PRECISION,

			-- Timestamps
			created_at       TIMESTAMPTZ      NOT NULL DEFAULT NOW(),

			-- Operator acknowledgment — NULL means not yet acknowledged
			acknowledged_at  TIMESTAMPTZ,
			acknowledged_by  TEXT,

			-- Constraint: alert_type must be one of the 3 valid values
			-- Prevents garbage data entering the table
			CONSTRAINT chk_alert_type CHECK (
				alert_type IN ('SPEEDING', 'LOW_FUEL', 'ENGINE_OVERHEAT')
			),

			-- Constraint: severity must be one of the 3 valid values
			CONSTRAINT chk_severity CHECK (
				severity IN ('INFO', 'WARNING', 'CRITICAL')
			)
		);
	`, "vehicle_alerts table created")
}

// ─────────────────────────────────────────────────────────────
// Step 4 — Indexes
// ─────────────────────────────────────────────────────────────
func step4_indexes(ctx context.Context, conn *pgx.Conn) {
	fmt.Println("\n── Step 4: Indexes ─────────────────────────────")

	indexes := []struct {
		name string
		sql  string
		why  string
	}{
		{
			name: "idx_telemetry_vehicle_time",
			sql: `CREATE INDEX IF NOT EXISTS idx_telemetry_vehicle_time
				  ON vehicle_telemetry (vehicle_id, timestamp DESC);`,
			why: "query: telemetry history for one vehicle",
		},
		{
			name: "idx_telemetry_fleet_time",
			sql: `CREATE INDEX IF NOT EXISTS idx_telemetry_fleet_time
				  ON vehicle_telemetry (fleet_id, timestamp DESC);`,
			why: "query: all vehicles in a fleet",
		},
		{
			name: "idx_telemetry_location",
			sql: `CREATE INDEX IF NOT EXISTS idx_telemetry_location
				  ON vehicle_telemetry USING GIST (location);`,
			why: "query: vehicles near a lat/lng (ST_DWithin)",
		},
		{
			name: "idx_alerts_vehicle",
			sql: `CREATE INDEX IF NOT EXISTS idx_alerts_vehicle
				  ON vehicle_alerts (vehicle_id, created_at DESC);`,
			why: "query: alerts for one vehicle",
		},
		{
			name: "idx_alerts_fleet",
			sql: `CREATE INDEX IF NOT EXISTS idx_alerts_fleet
				  ON vehicle_alerts (fleet_id, created_at DESC);`,
			why: "query: all alerts in a fleet",
		},
		{
			name: "idx_alerts_unacknowledged",
			sql: `CREATE INDEX IF NOT EXISTS idx_alerts_unacknowledged
				  ON vehicle_alerts (fleet_id, created_at DESC)
				  WHERE acknowledged_at IS NULL;`,
			why: "query: unacknowledged alerts only (partial index)",
		},
	}

	for _, idx := range indexes {
		execOrFatal(ctx, conn, idx.sql,
			fmt.Sprintf("%-40s ← %s", idx.name, idx.why),
		)
	}
}

// ─────────────────────────────────────────────────────────────
// Step 5 — Verify everything was created
// ─────────────────────────────────────────────────────────────
func step5_verify(ctx context.Context, conn *pgx.Conn) {
	fmt.Println("\n── Step 5: Verification ────────────────────────")

	// Check tables exist
	tables := []string{"vehicle_telemetry", "vehicle_alerts"}
	for _, table := range tables {
		var exists bool
		err := conn.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM information_schema.tables
				WHERE table_name = $1
			)
		`, table).Scan(&exists)
		if err != nil || !exists {
			log.Fatalf("Table %s was not created: %v", table, err)
		}
		fmt.Printf("  ✓ table: %s\n", table)
	}

	// Check hypertable
	var hypertableName string
	err := conn.QueryRow(ctx, `
		SELECT hypertable_name
		FROM timescaledb_information.hypertables
		WHERE hypertable_name = 'vehicle_telemetry'
	`).Scan(&hypertableName)
	if err != nil {
		log.Fatalf("vehicle_telemetry is not a hypertable: %v", err)
	}
	fmt.Printf("  ✓ hypertable: %s (time partitioned)\n", hypertableName)

	// Check indexes
	var indexCount int
	err = conn.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM pg_indexes
		WHERE tablename IN ('vehicle_telemetry', 'vehicle_alerts')
		AND indexname LIKE 'idx_%'
	`).Scan(&indexCount)
	if err != nil {
		log.Fatalf("Index check failed: %v", err)
	}
	fmt.Printf("  ✓ indexes created: %d\n", indexCount)
}

// ─────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────

// execOrFatal runs a SQL statement and prints result or exits on error
func execOrFatal(ctx context.Context, conn *pgx.Conn, sql, label string) {
	_, err := conn.Exec(ctx, sql)
	if err != nil {
		log.Fatalf("FAILED — %s\nError: %v\nSQL: %s", label, err, sql)
	}
	fmt.Printf("  ✓ %s\n", label)
}

func dbGetEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
