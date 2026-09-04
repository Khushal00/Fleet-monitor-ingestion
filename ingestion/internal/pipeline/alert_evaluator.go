package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"fleet-monitor/ingestion/internal/domain"
	"fleet-monitor/ingestion/internal/store"
)

type AlertEvaluator struct {
	ch    <-chan *domain.TelemetryMessage
	db    alertStore
	redis alertDedupStore
	rules []domain.AlertRule
}

type alertStore interface {
	InsertAlert(context.Context, string, string, domain.AlertType, domain.AlertSeverity, float64) error
}

type alertDedupStore interface {
	TryClaimAlertDedup(context.Context, string, domain.AlertType) (bool, error)
	ReleaseAlertDedup(context.Context, string, domain.AlertType) error
	PublishAlert(context.Context, string, []byte) error
}

func NewAlertEvaluator(
	ch <-chan *domain.TelemetryMessage,
	db *store.TimescaleStore,
	redis *store.RedisStore,
) *AlertEvaluator {
	return &AlertEvaluator{
		ch:    ch,
		db:    db,
		redis: redis,
		rules: domain.DefaultAlertRules,
	}
}

func (e *AlertEvaluator) Run(ctx context.Context) {
	for {
		select {
		case msg, ok := <-e.ch:
			if !ok {
				return
			}
			e.evaluate(context.Background(), msg)

		case <-ctx.Done():
			return
		}
	}
}

func (e *AlertEvaluator) evaluate(ctx context.Context, msg *domain.TelemetryMessage) {
	for _, rule := range e.rules {
		if !rule.Evaluator(msg) {
			continue
		}

		claimed, err := e.redis.TryClaimAlertDedup(ctx, msg.VehicleID, rule.Type)
		if err != nil {
			fmt.Printf("Alert dedup claim failed for %s/%s: %v\n", msg.VehicleID, rule.Type, err)
			continue
		}
		if !claimed {
			continue
		}

		triggerValue := e.getTriggerValue(msg, rule.Type)

		err = e.db.InsertAlert(ctx, msg.VehicleID, msg.FleetID, rule.Type, rule.Severity, triggerValue)
		if err != nil {
			fmt.Printf("Alert insert failed for %s: %v\n", msg.VehicleID, err)
			if releaseErr := e.redis.ReleaseAlertDedup(ctx, msg.VehicleID, rule.Type); releaseErr != nil {
				fmt.Printf("Alert dedup release failed for %s/%s: %v\n", msg.VehicleID, rule.Type, releaseErr)
			}
			continue
		}

		alertPayload, _ := json.Marshal(map[string]interface{}{
			"vehicle_id":   msg.VehicleID,
			"fleet_id":     msg.FleetID,
			"alert_type":   string(rule.Type),
			"severity":     string(rule.Severity),
			"value":        triggerValue,
			"triggered_at": time.Now().Unix(),
		})
		e.redis.PublishAlert(ctx, msg.FleetID, alertPayload)
	}
}

func (e *AlertEvaluator) getTriggerValue(msg *domain.TelemetryMessage, t domain.AlertType) float64 {
	switch t {
	case domain.AlertSpeeding:
		return msg.SpeedKmh
	case domain.AlertLowFuel:
		return msg.FuelPct
	case domain.AlertEngineOverheat:
		return msg.EngineTempC
	default:
		return 0
	}
}
