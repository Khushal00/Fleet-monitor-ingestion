package domain

import "time"

type TelemetryMessage struct {
	ReceivedAt time.Time

	Timestamp time.Time
	VehicleID string
	FleetID   string

	Latitude  float64
	Longitude float64

	SpeedKmh       float64
	FuelPct        float64
	EngineTempC    float64
	BatteryVoltage float64
	OdometerKm     float64
	IsMoving       bool
	EngineOn       bool

	RawPayload []byte
}

type AlertType string

const (
	AlertSpeeding       AlertType = "SPEEDING"
	AlertLowFuel        AlertType = "LOW_FUEL"
	AlertEngineOverheat AlertType = "ENGINE_OVERHEAT"
)

type AlertSeverity string

const (
	SeverityInfo     AlertSeverity = "INFO"
	SeverityWarning  AlertSeverity = "WARNING"
	SeverityCritical AlertSeverity = "CRITICAL"
)

type AlertRule struct {
	Type      AlertType
	Severity  AlertSeverity
	Evaluator func(msg *TelemetryMessage) bool
}

var DefaultAlertRules = []AlertRule{
	{
		Type:     AlertSpeeding,
		Severity: SeverityWarning,
		Evaluator: func(m *TelemetryMessage) bool {
			return m.SpeedKmh > 100.0
		},
	},
	{
		Type:     AlertLowFuel,
		Severity: SeverityWarning,
		Evaluator: func(m *TelemetryMessage) bool {
			return m.FuelPct < 10.0
		},
	},
	{
		Type:     AlertEngineOverheat,
		Severity: SeverityCritical,
		Evaluator: func(m *TelemetryMessage) bool {
			return m.EngineTempC > 100.0
		},
	},
}
