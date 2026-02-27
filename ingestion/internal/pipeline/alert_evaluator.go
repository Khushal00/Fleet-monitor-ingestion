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
	db    *store.TimescaleStore
	redis *store.RedisStore
	rules []domain.AlertRule
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

		isDuplicate, err := e.redis.CheckAlertDedup(ctx, msg.VehicleID, rule.Type)
		if err != nil {
			fmt.Printf("Alert dedup check failed for %s/%s: %v\n", msg.VehicleID, rule.Type, err)
			continue
		}
		if isDuplicate {
			continue
		}

		triggerValue := e.getTriggerValue(msg, rule.Type)

		err = e.db.InsertAlert(ctx, msg.VehicleID, msg.FleetID, rule.Type, rule.Severity, triggerValue)
		if err != nil {
			fmt.Printf("Alert insert failed for %s: %v\n", msg.VehicleID, err)
			continue
		}

		if err := e.redis.SetAlertDedup(ctx, msg.VehicleID, rule.Type); err != nil {
			fmt.Printf("Alert dedup set failed for %s: %v\n", msg.VehicleID, err)
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
