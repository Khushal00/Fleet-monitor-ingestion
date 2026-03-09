package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"fleet-monitor/serving/internal/domain"
)

// VehicleHandler serves all vehicle-centric endpoints:
//
//	GET /api/v1/vehicles/{vehicle_id}/panel        — full drill-down panel
//	GET /api/v1/vehicles/{vehicle_id}/active-trip  — shortcut used by the panel
//	GET /api/v1/vehicles/{vehicle_id}/alerts       — paginated alert history
type VehicleHandler struct {
	redis   *redis.Client
	tsStore *pgxpool.Pool // single TimescaleDB pool — all tables live here
}

func NewVehicleHandler(redisClient *redis.Client, tsStore *pgxpool.Pool) *VehicleHandler {
	return &VehicleHandler{
		redis:   redisClient,
		tsStore: tsStore,
	}
}

// ── Response types ────────────────────────────────────────────────────────────

// PanelResponse is the full payload for the vehicle drill-down side panel.
// Assembled from Redis + TimescaleDB in a single HTTP request.
type PanelResponse struct {
	VehicleID    string           `json:"vehicle_id"`
	LiveState    LiveTelemetry    `json:"live_telemetry"`
	Identity     *VehicleIdentity `json:"identity,omitempty"`
	Driver       *DriverInfo      `json:"driver,omitempty"`
	ActiveTrip   *TripInfo        `json:"active_trip,omitempty"`
	RecentAlerts []domain.Alert   `json:"recent_alerts"`
}

// LiveTelemetry is the real-time vehicle data shown at the top of the panel.
type LiveTelemetry struct {
	SpeedKmh     float64 `json:"speed_kmh"`
	FuelPct      float64 `json:"fuel_pct"`
	EngineTempC  float64 `json:"engine_temp_celsius"`
	Battery      float64 `json:"battery_voltage"`
	IsMoving     bool    `json:"is_moving"`
	EngineOn     bool    `json:"engine_on"`
	LastSeenAt   *int64  `json:"last_seen_at"` // unix timestamp, nil if never seen
	OnlineStatus string  `json:"online_status"`
}

// VehicleIdentity is the static registry data shown in the panel identity section.
type VehicleIdentity struct {
	DisplayName        string   `json:"display_name"`
	RegistrationNumber string   `json:"registration_number"`
	VehicleType        string   `json:"vehicle_type"`
	CapacityTonnes     *float64 `json:"capacity_tonnes,omitempty"`
}

// DriverInfo is shown only when an active trip exists for this vehicle.
type DriverInfo struct {
	DriverID      string `json:"driver_id"`
	FullName      string `json:"full_name"`
	PhoneNumber   string `json:"phone_number"`
	LicenseNumber string `json:"license_number"`
}

// TripInfo is the active trip summary shown in the panel trip section.
type TripInfo struct {
	TripID          string                `json:"trip_id"`
	RouteName       string                `json:"route_name"`
	OriginName      string                `json:"origin_name"`
	DestinationName string                `json:"destination_name"`
	Status          domain.TripStatus     `json:"status"`
	StopsTotal      int                   `json:"stops_total"`
	StopsCompleted  int                   `json:"stops_completed"`
	DeviationStatus string                `json:"deviation_status"` // "on_route" | "deviated"
	ETA             *string               `json:"eta,omitempty"`
	Stops           []domain.StopProgress `json:"stops"`
}

// ── Handlers ──────────────────────────────────────────────────────────────────

// GET /api/v1/vehicles/{vehicle_id}/panel
func (h *VehicleHandler) HandlePanel(w http.ResponseWriter, r *http.Request) {
	vehicleID := r.PathValue("vehicle_id")
	if vehicleID == "" {
		writeError(w, http.StatusBadRequest, "vehicle_id path parameter is required")
		return
	}

	ctx := r.Context()
	panel := PanelResponse{VehicleID: vehicleID}

	// ── Live telemetry from Redis ────────────────────────────────────────────
	stateKey := fmt.Sprintf("vehicle:%s:state", vehicleID)
	stateMap, err := h.redis.HGetAll(ctx, stateKey).Result()
	if err == nil && len(stateMap) > 0 {
		panel.LiveState.SpeedKmh = parseMapFloat(stateMap, "speed_kmh")
		panel.LiveState.FuelPct = parseMapFloat(stateMap, "fuel_pct")
		panel.LiveState.EngineTempC = parseMapFloat(stateMap, "engine_temp")
		panel.LiveState.Battery = parseMapFloat(stateMap, "battery")
		panel.LiveState.IsMoving = parseMapBool(stateMap, "is_moving")
		panel.LiveState.EngineOn = parseMapBool(stateMap, "engine_on")
		if ts, ok := stateMap["received_at"]; ok {
			if n, e := strconv.ParseInt(ts, 10, 64); e == nil {
				panel.LiveState.LastSeenAt = &n
			}
		}
	}

	onlineStatus, _ := h.redis.Get(ctx, fmt.Sprintf("vehicle:%s:online_status", vehicleID)).Result()
	if onlineStatus == "" {
		onlineStatus = string(domain.StatusOnline)
	}
	panel.LiveState.OnlineStatus = onlineStatus

	// ── Vehicle identity from TimescaleDB ────────────────────────────────────
	var identity VehicleIdentity
	var capTonnes *float64
	err = h.tsStore.QueryRow(ctx, `
		SELECT display_name, registration_number, vehicle_type, capacity_tonnes
		FROM vehicle_registry
		WHERE vehicle_id = $1
	`, vehicleID).Scan(
		&identity.DisplayName,
		&identity.RegistrationNumber,
		&identity.VehicleType,
		&capTonnes,
	)
	if err == nil {
		identity.CapacityTonnes = capTonnes
		panel.Identity = &identity
	}

	// ── Active trip from TimescaleDB ─────────────────────────────────────────
	var tripID, routeID, routeName, originName, destName string
	var tripStatus domain.TripStatus
	err = h.tsStore.QueryRow(ctx, `
		SELECT t.trip_id, t.route_id, r.route_name, r.origin_name, r.destination_name, t.status
		FROM trip t
		JOIN route_registry r ON r.route_id = t.route_id
		WHERE t.vehicle_id = $1 AND t.status = $2
		LIMIT 1
	`, vehicleID, domain.TripInProgress).Scan(
		&tripID, &routeID, &routeName, &originName, &destName, &tripStatus,
	)

	if err == nil {
		tripInfo := TripInfo{
			TripID:          tripID,
			RouteName:       routeName,
			OriginName:      originName,
			DestinationName: destName,
			Status:          tripStatus,
		}

		// Stop progress — LEFT JOIN so stops without progress rows default to PENDING
		rows, qErr := h.tsStore.Query(ctx, `
			SELECT rs.stop_id, rs.stop_name, rs.stop_sequence,
			       COALESCE(tsp.status, $1),
			       tsp.arrived_at, tsp.departed_at
			FROM route_stops rs
			LEFT JOIN trip_stop_progress tsp
			       ON tsp.stop_id = rs.stop_id AND tsp.trip_id = $2
			WHERE rs.route_id = $3
			ORDER BY rs.stop_sequence
		`, string(domain.StopPending), tripID, routeID)
		if qErr == nil {
			defer rows.Close()
			for rows.Next() {
				var sp domain.StopProgress
				var arrivedAt, departedAt *time.Time
				var statusStr string
				if sErr := rows.Scan(
					&sp.StopID, &sp.StopName, &sp.Sequence,
					&statusStr, &arrivedAt, &departedAt,
				); sErr == nil {
					sp.Status = domain.StopStatus(statusStr)
					if arrivedAt != nil {
						s := arrivedAt.Format(time.RFC3339)
						sp.ArrivedAt = &s
					}
					if departedAt != nil {
						s := departedAt.Format(time.RFC3339)
						sp.DepartedAt = &s
					}
					tripInfo.Stops = append(tripInfo.Stops, sp)
					tripInfo.StopsTotal++
					if sp.Status == domain.StopArrived || sp.Status == domain.StopDeparted {
						tripInfo.StopsCompleted++
					}
				}
			}
		}
		if tripInfo.Stops == nil {
			tripInfo.Stops = []domain.StopProgress{}
		}

		// Deviation status from Redis
		devVal, _ := h.redis.Get(ctx, fmt.Sprintf("vehicle:%s:deviation", vehicleID)).Result()
		if devVal == "true" {
			tripInfo.DeviationStatus = "deviated"
		} else {
			tripInfo.DeviationStatus = "on_route"
		}

		// ETA from Redis trip:{id}:eta
		if etaVal, e := h.redis.Get(ctx, fmt.Sprintf("trip:%s:eta", tripID)).Result(); e == nil {
			tripInfo.ETA = &etaVal
		}

		panel.ActiveTrip = &tripInfo

		// Driver info — only available when a trip is active
		var driverID, fullName, phone, license string
		err = h.tsStore.QueryRow(ctx, `
			SELECT d.driver_id, d.full_name, d.phone_number, d.license_number
			FROM driver_registry d
			JOIN trip t ON t.driver_id = d.driver_id
			WHERE t.trip_id = $1
		`, tripID).Scan(&driverID, &fullName, &phone, &license)
		if err == nil {
			panel.Driver = &DriverInfo{
				DriverID:      driverID,
				FullName:      fullName,
				PhoneNumber:   phone,
				LicenseNumber: license,
			}
		}
	}

	// ── Recent alerts from TimescaleDB — last 5 unacknowledged/unresolved ────
	alertRows, err := h.tsStore.Query(ctx, `
		SELECT id, alert_type, severity, triggered_value, created_at
		FROM vehicle_alerts
		WHERE vehicle_id = $1
		  AND acknowledged_at IS NULL
		  AND resolved_at IS NULL
		ORDER BY
		  CASE severity WHEN 'CRITICAL' THEN 0 WHEN 'WARNING' THEN 1 ELSE 2 END,
		  created_at DESC
		LIMIT 5
	`, vehicleID)
	if err == nil {
		defer alertRows.Close()
		for alertRows.Next() {
			var a domain.Alert
			var createdAt time.Time
			var alertTypeStr, severityStr string
			if e := alertRows.Scan(
				&a.ID, &alertTypeStr, &severityStr,
				&a.TriggerValue, &createdAt,
			); e == nil {
				a.AlertType = domain.AlertType(alertTypeStr)
				a.Severity = domain.AlertSeverity(severityStr)
				a.VehicleID = vehicleID
				a.CreatedAt = createdAt.Format(time.RFC3339)
				panel.RecentAlerts = append(panel.RecentAlerts, a)
			}
		}
	}
	if panel.RecentAlerts == nil {
		panel.RecentAlerts = []domain.Alert{}
	}

	writeJSON(w, http.StatusOK, panel)
}

// GET /api/v1/vehicles/{vehicle_id}/active-trip
func (h *VehicleHandler) HandleActiveTrip(w http.ResponseWriter, r *http.Request) {
	vehicleID := r.PathValue("vehicle_id")
	if vehicleID == "" {
		writeError(w, http.StatusBadRequest, "vehicle_id path parameter is required")
		return
	}

	ctx := r.Context()

	var tripID, routeName, originName, destName string
	var tripStatus domain.TripStatus
	err := h.tsStore.QueryRow(ctx, `
		SELECT t.trip_id, r.route_name, r.origin_name, r.destination_name, t.status
		FROM trip t
		JOIN route_registry r ON r.route_id = t.route_id
		WHERE t.vehicle_id = $1 AND t.status = $2
		LIMIT 1
	`, vehicleID, domain.TripInProgress).Scan(
		&tripID, &routeName, &originName, &destName, &tripStatus,
	)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"vehicle_id":  vehicleID,
			"active_trip": nil,
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"vehicle_id": vehicleID,
		"active_trip": map[string]string{
			"trip_id":          tripID,
			"route_name":       routeName,
			"origin_name":      originName,
			"destination_name": destName,
			"status":           string(tripStatus),
		},
	})
}

// GET /api/v1/vehicles/{vehicle_id}/alerts
func (h *VehicleHandler) HandleVehicleAlerts(w http.ResponseWriter, r *http.Request) {
	vehicleID := r.PathValue("vehicle_id")
	if vehicleID == "" {
		writeError(w, http.StatusBadRequest, "vehicle_id path parameter is required")
		return
	}

	ctx := r.Context()
	q := r.URL.Query()

	page, limit := parsePagination(q.Get("page"), q.Get("limit"))
	offset := (page - 1) * limit

	args := []interface{}{vehicleID}
	where := "WHERE vehicle_id = $1"

	if v := q.Get("from"); v != "" {
		if t, e := time.Parse(time.RFC3339, v); e == nil {
			args = append(args, t)
			where += fmt.Sprintf(" AND created_at >= $%d", len(args))
		}
	}
	if v := q.Get("to"); v != "" {
		if t, e := time.Parse(time.RFC3339, v); e == nil {
			args = append(args, t)
			where += fmt.Sprintf(" AND created_at <= $%d", len(args))
		}
	}
	if v := q.Get("alert_type"); v != "" {
		args = append(args, v)
		where += fmt.Sprintf(" AND alert_type = $%d", len(args))
	}
	if v := q.Get("severity"); v != "" {
		args = append(args, v)
		where += fmt.Sprintf(" AND severity = $%d", len(args))
	}

	var total int
	_ = h.tsStore.QueryRow(ctx, "SELECT COUNT(*) FROM vehicle_alerts "+where, args...).Scan(&total)

	args = append(args, limit, offset)
	rows, err := h.tsStore.Query(ctx, `
		SELECT id, alert_type, severity, triggered_value, created_at,
		       acknowledged_at, acknowledged_by, resolved_at, resolved_by
		FROM vehicle_alerts
		`+where+`
		ORDER BY created_at DESC
		LIMIT $`+fmt.Sprintf("%d", len(args)-1)+` OFFSET $`+fmt.Sprintf("%d", len(args)),
		args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query alert history")
		return
	}
	defer rows.Close()

	alerts := []domain.Alert{}
	for rows.Next() {
		var a domain.Alert
		var createdAt time.Time
		var ackAt, resolvedAt *time.Time
		var alertTypeStr, severityStr string
		if e := rows.Scan(
			&a.ID, &alertTypeStr, &severityStr, &a.TriggerValue,
			&createdAt, &ackAt, &a.AcknowledgedBy, &resolvedAt, &a.ResolvedBy,
		); e == nil {
			a.AlertType = domain.AlertType(alertTypeStr)
			a.Severity = domain.AlertSeverity(severityStr)
			a.VehicleID = vehicleID
			a.CreatedAt = createdAt.Format(time.RFC3339)
			if ackAt != nil {
				s := ackAt.Format(time.RFC3339)
				a.AcknowledgedAt = &s
			}
			if resolvedAt != nil {
				s := resolvedAt.Format(time.RFC3339)
				a.ResolvedAt = &s
			}
			alerts = append(alerts, a)
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"vehicle_id": vehicleID,
		"alerts":     alerts,
		"pagination": domain.NewPagination(page, limit, total),
	})
}
