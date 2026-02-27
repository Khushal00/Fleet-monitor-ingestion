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
	stateData := map[string]interface{}{
		"vehicle_id":  msg.VehicleID,
		"fleet_id":    msg.FleetID,
		"lat":         msg.Latitude,
		"lng":         msg.Longitude,
		"speed_kmh":   msg.SpeedKmh,
		"fuel_pct":    msg.FuelPct,
		"engine_temp": msg.EngineTempC,
		"battery":     msg.BatteryVoltage,
		"is_moving":   msg.IsMoving,
		"engine_on":   msg.EngineOn,
		"timestamp":   msg.Timestamp.Unix(),
		"received_at": msg.ReceivedAt.Unix(),
	}

	pubPayload, err := json.Marshal(stateData)
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	vehicleStateKey := fmt.Sprintf("vehicle:%s:state", msg.VehicleID)
	geoKey := fmt.Sprintf("fleet:%s:geo", msg.FleetID)
	pubChannel := fmt.Sprintf("fleet:%s:telemetry", msg.FleetID)

	pipe := r.client.Pipeline()

	pipe.HSet(ctx, vehicleStateKey, stateData)
	pipe.Expire(ctx, vehicleStateKey, 30*time.Second)
	pipe.GeoAdd(ctx, geoKey, &redis.GeoLocation{
		Name:      msg.VehicleID,
		Longitude: msg.Longitude,
		Latitude:  msg.Latitude,
	})
	pipe.Publish(ctx, pubChannel, pubPayload)

	_, err = pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("redis pipeline failed: %w", err)
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

func (r *RedisStore) CheckAlertDedup(ctx context.Context, vehicleID string, alertType domain.AlertType) (bool, error) {
	key := fmt.Sprintf("alert:%s:%s", vehicleID, string(alertType))
	count, err := r.client.Exists(ctx, key).Result()
	if err != nil {
		return false, fmt.Errorf("dedup check failed: %w", err)
	}
	return count > 0, nil
}

func (r *RedisStore) SetAlertDedup(ctx context.Context, vehicleID string, alertType domain.AlertType) error {
	key := fmt.Sprintf("alert:%s:%s", vehicleID, string(alertType))
	return r.client.Set(ctx, key, "1", 5*time.Minute).Err()
}

func (r *RedisStore) PublishAlert(ctx context.Context, fleetID string, payload []byte) error {
	channel := fmt.Sprintf("fleet:%s:alerts", fleetID)
	return r.client.Publish(ctx, channel, payload).Err()
}
