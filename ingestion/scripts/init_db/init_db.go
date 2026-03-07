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
	step4_registry_tables(ctx, conn)
	step5_indexes(ctx, conn)
	step6_verify(ctx, conn)

	fmt.Println("\n Database initialised successfully")
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
			-- SPEEDING | LOW_FUEL | ENGINE_OVERHEAT | ROUTE_DEVIATION
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

			-- Operator resolution — separate from acknowledgment.
			-- acknowledged_at = "I am handling this"
			-- resolved_at     = "the condition is confirmed cleared"
			resolved_at      TIMESTAMPTZ,
			resolved_by      TEXT,

			-- Constraint: alert_type must be one of the 4 valid values
			-- ROUTE_DEVIATION is fired by the route deviation detector background job
			CONSTRAINT chk_alert_type CHECK (
				alert_type IN ('SPEEDING', 'LOW_FUEL', 'ENGINE_OVERHEAT', 'ROUTE_DEVIATION')
			),

			-- Constraint: severity must be one of the 3 valid values
			CONSTRAINT chk_severity CHECK (
				severity IN ('INFO', 'WARNING', 'CRITICAL')
			)
		);
	`, "vehicle_alerts table created")
}

// ─────────────────────────────────────────────────────────────
// Step 4 — Registry and trip tables
// ─────────────────────────────────────────────────────────────
// Order matters — tables with foreign keys must come after the
// tables they reference:
//
//	vehicle_registry, driver_registry, route_registry  (no FKs)
//	route_stops       → route_registry
//	fleet_config      (no FKs)
//	trip              → vehicle_registry, driver_registry, route_registry
//	trip_stop_progress → trip, route_stops
//
// ─────────────────────────────────────────────────────────────
func step4_registry_tables(ctx context.Context, conn *pgx.Conn) {
	fmt.Println("\n── Step 4: Registry and trip tables ────────────")

	// vehicle_registry — one row per physical truck.
	// vehicle_id must match exactly the string the GPS device sends in telemetry.
	// active=false retires a vehicle without deleting its telemetry history.
	execOrFatal(ctx, conn, `
		CREATE TABLE IF NOT EXISTS vehicle_registry (
			vehicle_id          TEXT             PRIMARY KEY,
			fleet_id            TEXT             NOT NULL,
			display_name        TEXT             NOT NULL,
			registration_number TEXT             NOT NULL,
			vehicle_type        TEXT             NOT NULL,
			capacity_tonnes     NUMERIC,
			manufacture_year    INT,
			active              BOOLEAN          NOT NULL DEFAULT true
		);
	`, "vehicle_registry table created")

	// driver_registry — one row per driver.
	// Drivers are assigned to vehicles per trip, not permanently.
	// phone_number is exposed in the drill-down panel so the operations
	// manager can call directly from the dashboard.
	execOrFatal(ctx, conn, `
		CREATE TABLE IF NOT EXISTS driver_registry (
			driver_id       TEXT    PRIMARY KEY,
			full_name       TEXT    NOT NULL,
			phone_number    TEXT    NOT NULL,
			license_number  TEXT    NOT NULL,
			license_expiry  DATE    NOT NULL,
			active          BOOLEAN NOT NULL DEFAULT true
		);
	`, "driver_registry table created")

	// route_registry — reusable route templates.
	// A route defines origin, destination, and corridor.
	// corridor_radius_km controls how far off the polyline before a
	// ROUTE_DEVIATION alert fires.
	execOrFatal(ctx, conn, `
		CREATE TABLE IF NOT EXISTS route_registry (
			route_id             TEXT             PRIMARY KEY,
			route_name           TEXT             NOT NULL,
			origin_name          TEXT             NOT NULL,
			origin_lat           DOUBLE PRECISION NOT NULL,
			origin_lng           DOUBLE PRECISION NOT NULL,
			destination_name     TEXT             NOT NULL,
			destination_lat      DOUBLE PRECISION NOT NULL,
			destination_lng      DOUBLE PRECISION NOT NULL,
			corridor_radius_km   NUMERIC          NOT NULL DEFAULT 25,
			total_distance_km    NUMERIC,
			active               BOOLEAN          NOT NULL DEFAULT true
		);
	`, "route_registry table created")

	// route_stops — ordered stops along a route.
	// stop_sequence is 1-based; the destination is the final stop.
	// arrival_radius_km: vehicle within this distance = counted as arrived.
	execOrFatal(ctx, conn, `
		CREATE TABLE IF NOT EXISTS route_stops (
			stop_id           TEXT             PRIMARY KEY,
			route_id          TEXT             NOT NULL REFERENCES route_registry(route_id),
			stop_sequence     INT              NOT NULL,
			stop_name         TEXT             NOT NULL,
			lat               DOUBLE PRECISION NOT NULL,
			lng               DOUBLE PRECISION NOT NULL,
			arrival_radius_km NUMERIC          NOT NULL DEFAULT 1
		);
	`, "route_stops table created")

	// fleet_config — per-fleet operational config.
	// staleness_threshold_seconds: how long a vehicle can go silent before
	// the heartbeat monitor marks it offline. Default 60s.
	execOrFatal(ctx, conn, `
		CREATE TABLE IF NOT EXISTS fleet_config (
			fleet_id                    TEXT PRIMARY KEY,
			staleness_threshold_seconds INT  NOT NULL DEFAULT 60
		);
	`, "fleet_config table created")

	// trip — a specific instance of a vehicle + driver + route.
	// This is the central entity that ties everything together for
	// the stop detector, deviation detector, and ETA estimator.
	execOrFatal(ctx, conn, `
		CREATE TABLE IF NOT EXISTS trip (
			trip_id              TEXT        PRIMARY KEY,
			vehicle_id           TEXT        NOT NULL REFERENCES vehicle_registry(vehicle_id),
			driver_id            TEXT        NOT NULL REFERENCES driver_registry(driver_id),
			route_id             TEXT        NOT NULL REFERENCES route_registry(route_id),
			status               TEXT        NOT NULL DEFAULT 'SCHEDULED',
			scheduled_departure  TIMESTAMPTZ NOT NULL,
			actual_departure     TIMESTAMPTZ,
			completed_at         TIMESTAMPTZ,
			created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),

			-- status must be one of the 4 lifecycle values
			CONSTRAINT chk_trip_status CHECK (
				status IN ('SCHEDULED', 'IN_PROGRESS', 'COMPLETED', 'CANCELLED')
			)
		);
	`, "trip table created")

	// trip_stop_progress — live scratchpad written by the stop detector job.
	// One row per (trip, stop) pair, initialised as PENDING when a trip starts.
	// Composite PK prevents duplicate rows for the same trip+stop.
	execOrFatal(ctx, conn, `
		CREATE TABLE IF NOT EXISTS trip_stop_progress (
			trip_id     TEXT        NOT NULL REFERENCES trip(trip_id),
			stop_id     TEXT        NOT NULL REFERENCES route_stops(stop_id),
			status      TEXT        NOT NULL DEFAULT 'PENDING',
			arrived_at  TIMESTAMPTZ,
			departed_at TIMESTAMPTZ,

			PRIMARY KEY (trip_id, stop_id),

			CONSTRAINT chk_stop_status CHECK (
				status IN ('PENDING', 'ARRIVED', 'DEPARTED', 'MISSED')
			)
		);
	`, "trip_stop_progress table created")
}

// ─────────────────────────────────────────────────────────────
// Step 5 — Indexes
// ─────────────────────────────────────────────────────────────
func step5_indexes(ctx context.Context, conn *pgx.Conn) {
	fmt.Println("\n── Step 5: Indexes ─────────────────────────────")

	indexes := []struct {
		name string
		sql  string
		why  string
	}{
		// ── vehicle_telemetry ────────────────────────────────────────
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

		// ── vehicle_alerts ───────────────────────────────────────────
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

		// ── route_stops ──────────────────────────────────────────────
		{
			name: "idx_route_stops_route",
			sql: `CREATE INDEX IF NOT EXISTS idx_route_stops_route
				  ON route_stops (route_id, stop_sequence);`,
			why: "query: all stops for a route in order",
		},

		// ── trip ─────────────────────────────────────────────────────
		{
			name: "idx_trip_vehicle",
			sql: `CREATE INDEX IF NOT EXISTS idx_trip_vehicle
				  ON trip (vehicle_id, status);`,
			why: "query: active trip for a vehicle (drill-down panel, stop detector)",
		},
		{
			name: "idx_trip_fleet_status",
			sql: `CREATE INDEX IF NOT EXISTS idx_trip_fleet_status
				  ON trip (route_id, status);`,
			why: "query: all in-progress trips (stop detector, deviation detector)",
		},

		// ── trip_stop_progress ───────────────────────────────────────
		{
			name: "idx_trip_stop_progress_trip",
			sql: `CREATE INDEX IF NOT EXISTS idx_trip_stop_progress_trip
				  ON trip_stop_progress (trip_id, status);`,
			why: "query: pending stops for a trip (stop detector)",
		},
	}

	for _, idx := range indexes {
		execOrFatal(ctx, conn, idx.sql,
			fmt.Sprintf("%-40s ← %s", idx.name, idx.why),
		)
	}
}

// ─────────────────────────────────────────────────────────────
// Step 6 — Verify everything was created
// ─────────────────────────────────────────────────────────────
func step6_verify(ctx context.Context, conn *pgx.Conn) {
	fmt.Println("\n── Step 6: Verification ────────────────────────")

	// Check all tables exist
	tables := []string{
		"vehicle_telemetry",
		"vehicle_alerts",
		"vehicle_registry",
		"driver_registry",
		"route_registry",
		"route_stops",
		"fleet_config",
		"trip",
		"trip_stop_progress",
	}
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

	// Check indexes across all relevant tables
	var indexCount int
	err = conn.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM pg_indexes
		WHERE tablename IN (
			'vehicle_telemetry', 'vehicle_alerts',
			'route_stops', 'trip', 'trip_stop_progress'
		)
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
