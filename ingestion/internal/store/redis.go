package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"fleet-monitor/ingestion/internal/config"
	"fleet-monitor/ingestion/internal/domain"
)

type RedisStore struct {
	client *redis.Client
}

func NewRedisStore(ctx context.Context, cfg *config.Config) (*RedisStore, error) {
	client := redis.NewClient(&redis.Options{
		Addr:         cfg.RedisAddr,
		Password:     cfg.RedisPassword,
		DB:           cfg.RedisDB,
		PoolSize:     20,
		MinIdleConns: 5,
	})

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to redis: %w", err)
	}

	return &RedisStore{client: client}, nil
}

func (r *RedisStore) Close() error {
	return r.client.Close()
}

func (r *RedisStore) Ping(ctx context.Context) error {
	return r.client.Ping(ctx).Err()
}

func (r *RedisStore) Client() *redis.Client {
	return r.client
}

func (r *RedisStore) PipelineStateUpdate(ctx context.Context, msg *domain.TelemetryMessage) error {
	return r.PipelineStateUpdates(ctx, []*domain.TelemetryMessage{msg})
}

// PipelineStateUpdates writes and publishes a complete state batch with one
// Redis pipeline execution, rather than one network round trip per message.
func (r *RedisStore) PipelineStateUpdates(ctx context.Context, msgs []*domain.TelemetryMessage) error {
	pipe := r.client.Pipeline()
	for _, msg := range msgs {
		stateData := map[string]interface{}{
			"vehicle_id": msg.VehicleID, "fleet_id": msg.FleetID,
			"lat": msg.Latitude, "lng": msg.Longitude,
			"speed_kmh": msg.SpeedKmh, "fuel_pct": msg.FuelPct,
			"engine_temp": msg.EngineTempC, "battery": msg.BatteryVoltage,
			"is_moving": msg.IsMoving, "engine_on": msg.EngineOn,
			"timestamp": msg.Timestamp.Unix(), "received_at": msg.ReceivedAt.Unix(),
		}
		pubPayload, err := json.Marshal(stateData)
		if err != nil {
			return fmt.Errorf("marshal state for %s: %w", msg.VehicleID, err)
		}
		pipe.HSet(ctx, fmt.Sprintf("vehicle:%s:state", msg.VehicleID), stateData)
		pipe.Expire(ctx, fmt.Sprintf("vehicle:%s:state", msg.VehicleID), 30*time.Second)
		pipe.Publish(ctx, fmt.Sprintf("fleet:%s:telemetry", msg.FleetID), pubPayload)
	}

	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("redis state pipeline failed: %w", err)
	}

	return nil
}

func (r *RedisStore) GetAPIKey(ctx context.Context, apiKey string) (string, error) {
	key := fmt.Sprintf("vehicle:auth:%s", apiKey)
	val, err := r.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("redis get api key failed: %w", err)
	}
	return val, nil
}

// TryClaimAlertDedup atomically claims an alert's five-minute deduplication
// window. A true result means this caller owns the alert and may persist it.
func (r *RedisStore) TryClaimAlertDedup(ctx context.Context, vehicleID string, alertType domain.AlertType) (bool, error) {
	key := fmt.Sprintf("alert:%s:%s", vehicleID, string(alertType))
	claimed, err := r.client.SetNX(ctx, key, "1", 5*time.Minute).Result()
	if err != nil {
		return false, fmt.Errorf("claim alert dedup window: %w", err)
	}
	return claimed, nil
}

// ReleaseAlertDedup removes a previously claimed deduplication key so a
// transient failure before persistence does not suppress the alert for the TTL.
func (r *RedisStore) ReleaseAlertDedup(ctx context.Context, vehicleID string, alertType domain.AlertType) error {
	key := fmt.Sprintf("alert:%s:%s", vehicleID, string(alertType))
	if err := r.client.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("release alert dedup window: %w", err)
	}
	return nil
}

func (r *RedisStore) PublishAlert(ctx context.Context, fleetID string, payload []byte) error {
	channel := fmt.Sprintf("fleet:%s:alerts", fleetID)
	return r.client.Publish(ctx, channel, payload).Err()
}
