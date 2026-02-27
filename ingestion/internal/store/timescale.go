package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"fleet-monitor/ingestion/internal/config"
	"fleet-monitor/ingestion/internal/domain"
)

type TimescaleStore struct {
	pool *pgxpool.Pool
}

func NewTimescaleStore(ctx context.Context, cfg *config.Config) (*TimescaleStore, error) {
	connStr := fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s?pool_max_conns=%d",
		cfg.DBUser,
		cfg.DBPassword,
		cfg.DBHost,
		cfg.DBPort,
		cfg.DBName,
		cfg.DBMaxConns,
	)

	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to create db pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping db: %w", err)
	}

	return &TimescaleStore{pool: pool}, nil
}

func (s *TimescaleStore) Close() {
	s.pool.Close()
}

func (s *TimescaleStore) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

var telemetryColumns = []string{
	"timestamp",
	"vehicle_id",
	"fleet_id",
	"latitude",
	"longitude",
	"speed_kmh",
	"fuel_pct",
	"engine_temp_celsius",
	"battery_voltage",
	"odometer_km",
	"is_moving",
	"engine_on",
	"raw_payload",
}

func (s *TimescaleStore) BatchInsert(ctx context.Context, msgs []*domain.TelemetryMessage) error {
	if len(msgs) == 0 {
		return nil
	}

	rows := make([][]interface{}, len(msgs))
	for i, m := range msgs {
		rows[i] = []interface{}{
			m.Timestamp,
			m.VehicleID,
			m.FleetID,
			m.Latitude,
			m.Longitude,
			m.SpeedKmh,
			m.FuelPct,
			m.EngineTempC,
			m.BatteryVoltage,
			m.OdometerKm,
			m.IsMoving,
			m.EngineOn,
			string(m.RawPayload),
		}
	}

	_, err := s.pool.CopyFrom(
		ctx,
		pgx.Identifier{"vehicle_telemetry"},
		telemetryColumns,
		pgx.CopyFromRows(rows),
	)
	if err != nil {
		return fmt.Errorf("CopyFrom failed for batch of %d: %w", len(msgs), err)
	}

	return nil
}

func (s *TimescaleStore) InsertAlert(
	ctx context.Context,
	vehicleID string,
	fleetID string,
	alertType domain.AlertType,
	severity domain.AlertSeverity,
	triggerValue float64,
) error {
	query := `
		INSERT INTO vehicle_alerts
			(vehicle_id, fleet_id, alert_type, severity, triggered_value, created_at)
		VALUES
			($1, $2, $3, $4, $5, NOW())
		ON CONFLICT DO NOTHING
	`
	_, err := s.pool.Exec(
		ctx,
		query,
		vehicleID,
		fleetID,
		string(alertType),
		string(severity),
		triggerValue,
	)
	return err
}
