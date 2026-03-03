package http

import (
	"encoding/json"
	"net/http"
	"time"

	"fleet-monitor/ingestion/internal/domain"
	"fleet-monitor/ingestion/internal/metrics"
	"fleet-monitor/ingestion/internal/pipeline"
)

type incomingPayload struct {
	Timestamp time.Time `json:"timestamp"`
	VehicleID string    `json:"vehicle_id"`
	FleetID   string    `json:"fleet_id"`
	Location  struct {
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
	} `json:"location"`
	VehicleState struct {
		SpeedKmh       float64 `json:"speed_kmh"`
		FuelPct        float64 `json:"fuel_pct"`
		EngineTempC    float64 `json:"engine_temp_celsius"`
		BatteryVoltage float64 `json:"battery_voltage"`
		OdometerKm     float64 `json:"odometer_km"`
		IsMoving       bool    `json:"is_moving"`
		EngineOn       bool    `json:"engine_on"`
	} `json:"vehicle_state"`
}

type TelemetryHandler struct {
	dispatcher *pipeline.Dispatcher
}

func NewTelemetryHandler(d *pipeline.Dispatcher) *TelemetryHandler {
	return &TelemetryHandler{dispatcher: d}
}

func (h *TelemetryHandler) Handle(w http.ResponseWriter, r *http.Request) {
	var p incomingPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid json payload"}`))
		return
	}

	if p.VehicleID == "" || p.FleetID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"vehicle_id and fleet_id are required"}`))
		return
	}

	raw, _ := json.Marshal(p)

	msg := &domain.TelemetryMessage{
		ReceivedAt:     time.Now().UTC(),
		Timestamp:      p.Timestamp,
		VehicleID:      p.VehicleID,
		FleetID:        p.FleetID,
		Latitude:       p.Location.Latitude,
		Longitude:      p.Location.Longitude,
		SpeedKmh:       p.VehicleState.SpeedKmh,
		FuelPct:        p.VehicleState.FuelPct,
		EngineTempC:    p.VehicleState.EngineTempC,
		BatteryVoltage: p.VehicleState.BatteryVoltage,
		OdometerKm:     p.VehicleState.OdometerKm,
		IsMoving:       p.VehicleState.IsMoving,
		EngineOn:       p.VehicleState.EngineOn,
		RawPayload:     raw,
	}

	h.dispatcher.Dispatch(msg)
	metrics.MessagesReceived.Add(1)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte(`{"status":"accepted"}`))
}
