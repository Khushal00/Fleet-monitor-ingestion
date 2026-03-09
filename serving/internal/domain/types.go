package domain

type VehicleState struct {
	VehicleID   string  `json:"vehicle_id"`
	FleetID     string  `json:"fleet_id"`
	Lat         float64 `json:"lat"`
	Lng         float64 `json:"lng"`
	SpeedKmh    float64 `json:"speed_kmh"`
	FuelPct     float64 `json:"fuel_pct"`
	EngineTempC float64 `json:"engine_temp_celsius"`
	Battery     float64 `json:"battery_voltage"`
	IsMoving    bool    `json:"is_moving"`
	EngineOn    bool    `json:"engine_on"`
	Timestamp   int64   `json:"timestamp"`   // unix — vehicle clock
	ReceivedAt  int64   `json:"received_at"` // unix — server receipt time
}

type VehicleRegistryRow struct {
	VehicleID          string   `json:"vehicle_id"`
	FleetID            string   `json:"fleet_id"`
	DisplayName        string   `json:"display_name"`
	RegistrationNumber string   `json:"registration_number"`
	VehicleType        string   `json:"vehicle_type"`
	CapacityTonnes     *float64 `json:"capacity_tonnes,omitempty"`  // nullable column
	ManufactureYear    *int     `json:"manufacture_year,omitempty"` // nullable column
	Active             bool     `json:"active"`
}

type VehicleOnlineStatus string

const (
	StatusOnline  VehicleOnlineStatus = "online"
	StatusOffline VehicleOnlineStatus = "offline"
)

type DriverRegistryRow struct {
	DriverID      string `json:"driver_id"`
	FullName      string `json:"full_name"`
	PhoneNumber   string `json:"phone_number"`
	LicenseNumber string `json:"license_number"`
	LicenseExpiry string `json:"license_expiry"` // date as string — "2026-12-01"
	Active        bool   `json:"active"`
}

type AlertType string

const (
	AlertSpeeding       AlertType = "SPEEDING"
	AlertLowFuel        AlertType = "LOW_FUEL"
	AlertEngineOverheat AlertType = "ENGINE_OVERHEAT"
	AlertRouteDeviation AlertType = "ROUTE_DEVIATION"
)

type AlertSeverity string

const (
	SeverityInfo     AlertSeverity = "INFO"
	SeverityWarning  AlertSeverity = "WARNING"
	SeverityCritical AlertSeverity = "CRITICAL"
)

type Alert struct {
	ID             int64         `json:"id"`
	VehicleID      string        `json:"vehicle_id"`
	FleetID        string        `json:"fleet_id"`
	AlertType      AlertType     `json:"alert_type"`
	Severity       AlertSeverity `json:"severity"`
	TriggerValue   float64       `json:"triggered_value"`
	CreatedAt      string        `json:"created_at"` // RFC3339
	AcknowledgedAt *string       `json:"acknowledged_at,omitempty"`
	AcknowledgedBy *string       `json:"acknowledged_by,omitempty"`
	ResolvedAt     *string       `json:"resolved_at,omitempty"`
	ResolvedBy     *string       `json:"resolved_by,omitempty"`
}

type TripStatus string

const (
	TripScheduled  TripStatus = "SCHEDULED"
	TripInProgress TripStatus = "IN_PROGRESS"
	TripCompleted  TripStatus = "COMPLETED"
	TripCancelled  TripStatus = "CANCELLED"
)

type StopStatus string

const (
	StopPending  StopStatus = "PENDING"
	StopArrived  StopStatus = "ARRIVED"
	StopDeparted StopStatus = "DEPARTED"
	StopMissed   StopStatus = "MISSED"
)

type StopProgress struct {
	StopID     string     `json:"stop_id"`
	StopName   string     `json:"stop_name"`
	Sequence   int        `json:"sequence"`
	Status     StopStatus `json:"status"`
	ArrivedAt  *string    `json:"arrived_at,omitempty"`
	DepartedAt *string    `json:"departed_at,omitempty"`
}

type Trip struct {
	TripID             string     `json:"trip_id"`
	VehicleID          string     `json:"vehicle_id"`
	DriverID           string     `json:"driver_id"`
	RouteID            string     `json:"route_id"`
	Status             TripStatus `json:"status"`
	ScheduledDeparture string     `json:"scheduled_departure"` // RFC3339
	ActualDeparture    *string    `json:"actual_departure,omitempty"`
	CompletedAt        *string    `json:"completed_at,omitempty"`
	CreatedAt          string     `json:"created_at"`
}

type RouteRegistryRow struct {
	RouteID          string  `json:"route_id"`
	RouteName        string  `json:"route_name"`
	OriginName       string  `json:"origin_name"`
	OriginLat        float64 `json:"origin_lat"`
	OriginLng        float64 `json:"origin_lng"`
	DestinationName  string  `json:"destination_name"`
	DestinationLat   float64 `json:"destination_lat"`
	DestinationLng   float64 `json:"destination_lng"`
	CorridorRadiusKm float64 `json:"corridor_radius_km"`
	TotalDistanceKm  float64 `json:"total_distance_km"`
	Active           bool    `json:"active"`
}

type PaginationMeta struct {
	Page  int `json:"page"`
	Limit int `json:"limit"`
	Total int `json:"total"`
	Pages int `json:"pages"`
}

func NewPagination(page, limit, total int) PaginationMeta {
	pages := 0
	if limit > 0 {
		pages = (total + limit - 1) / limit
	}
	return PaginationMeta{
		Page:  page,
		Limit: limit,
		Total: total,
		Pages: pages,
	}
}
