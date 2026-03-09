package ws

import "time"

type EventType string

const (
	EventVehiclePosition  EventType = "vehicle.position"
	EventVehicleAlert     EventType = "vehicle.alert"
	EventVehicleOffline   EventType = "vehicle.offline"
	EventVehicleOnline    EventType = "vehicle.online"
	EventVehicleDeviation EventType = "vehicle.deviation"
	EventStopArrived      EventType = "vehicle.stop_arrived"
	EventAlertResolved    EventType = "vehicle.alert_resolved"
	EventPing             EventType = "ping"
)

type envelope struct {
	Type    EventType   `json:"type"`
	Payload interface{} `json:"payload,omitempty"`
}

type VehiclePositionPayload struct {
	VehicleID  string    `json:"vehicle_id"`
	Lat        float64   `json:"lat"`
	Lng        float64   `json:"lng"`
	SpeedKmh   float64   `json:"speed_kmh"`
	IsMoving   bool      `json:"is_moving"`
	EngineOn   bool      `json:"engine_on"`
	ReceivedAt time.Time `json:"received_at"`
}

type VehicleAlertPayload struct {
	AlertID        int64     `json:"alert_id"`
	VehicleID      string    `json:"vehicle_id"`
	AlertType      string    `json:"alert_type"`
	Severity       string    `json:"severity"`
	TriggeredValue float64   `json:"triggered_value"`
	CreatedAt      time.Time `json:"created_at"`
}

type VehicleOfflinePayload struct {
	VehicleID  string    `json:"vehicle_id"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

type VehicleOnlinePayload struct {
	VehicleID string `json:"vehicle_id"`
}

type VehicleDeviationPayload struct {
	VehicleID   string  `json:"vehicle_id"`
	TripID      string  `json:"trip_id"`
	DeviationKm float64 `json:"deviation_km"`
}

type StopArrivedPayload struct {
	VehicleID string    `json:"vehicle_id"`
	TripID    string    `json:"trip_id"`
	StopID    string    `json:"stop_id"`
	StopName  string    `json:"stop_name"`
	ArrivedAt time.Time `json:"arrived_at"`
}

type AlertResolvedPayload struct {
	AlertID    int64     `json:"alert_id"`
	VehicleID  string    `json:"vehicle_id"`
	ResolvedAt time.Time `json:"resolved_at"`
}

func newPositionEvent(p VehiclePositionPayload) envelope {
	return envelope{Type: EventVehiclePosition, Payload: p}
}

func newAlertEvent(p VehicleAlertPayload) envelope {
	return envelope{Type: EventVehicleAlert, Payload: p}
}

func newOfflineEvent(p VehicleOfflinePayload) envelope {
	return envelope{Type: EventVehicleOffline, Payload: p}
}

func newOnlineEvent(p VehicleOnlinePayload) envelope {
	return envelope{Type: EventVehicleOnline, Payload: p}
}

func newDeviationEvent(p VehicleDeviationPayload) envelope {
	return envelope{Type: EventVehicleDeviation, Payload: p}
}

func newStopArrivedEvent(p StopArrivedPayload) envelope {
	return envelope{Type: EventStopArrived, Payload: p}
}

func newAlertResolvedEvent(p AlertResolvedPayload) envelope {
	return envelope{Type: EventAlertResolved, Payload: p}
}

func newPingEvent() envelope {
	return envelope{Type: EventPing}
}
