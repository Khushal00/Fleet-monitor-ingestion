package pipeline

import (
	"fleet-monitor/ingestion/internal/domain"
	"testing"
)

func TestDefaultAlertRulesRespectStrictThresholds(t *testing.T) {
	msg := &domain.TelemetryMessage{SpeedKmh: 100, FuelPct: 10, EngineTempC: 100}
	for _, rule := range domain.DefaultAlertRules {
		if rule.Evaluator(msg) {
			t.Fatalf("%s triggered at threshold", rule.Type)
		}
	}
	msg.SpeedKmh, msg.FuelPct, msg.EngineTempC = 100.1, 9.9, 100.1
	for _, rule := range domain.DefaultAlertRules {
		if !rule.Evaluator(msg) {
			t.Fatalf("%s did not trigger beyond threshold", rule.Type)
		}
	}
}
