package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"fleet-monitor/serving/internal/domain"
)

// AlertHandler serves all alert-management endpoints:
//
//	GET  /api/v1/alerts                          — attention queue (ranked, unacknowledged)
//	GET  /api/v1/alerts/{alert_id}               — full alert detail
//	POST /api/v1/alerts/{alert_id}/acknowledge   — mark as being handled
//	POST /api/v1/alerts/{alert_id}/resolve       — mark condition as cleared
//	POST /api/v1/alerts/{alert_id}/unacknowledge — reverse an erroneous acknowledge
//	GET  /api/v1/fleet/{fleet_id}/alerts         — paginated fleet alert history
type AlertHandler struct {
	redis   *redis.Client
	tsStore *pgxpool.Pool // single TimescaleDB pool — all tables live here
}

func NewAlertHandler(redisClient *redis.Client, tsStore *pgxpool.Pool) *AlertHandler {
	return &AlertHandler{
		redis:   redisClient,
		tsStore: tsStore,
	}
}

// enrichedAlert extends domain.Alert with fields that come from joins
// against the registry and active trip tables.
type enrichedAlert struct {
	domain.Alert
	DisplayName       string  `json:"display_name"`        // from vehicle_registry
	DriverName        *string `json:"driver_name"`         // from driver_registry via active trip
	RouteName         *string `json:"route_name"`          // from route_registry via active trip
	IsConditionActive bool    `json:"is_condition_active"` // cross-referenced from Redis
}

// ── Attention Queue ───────────────────────────────────────────────────────────

// GET /api/v1/alerts
//
// Query params: fleet_id (required), severity, alert_type, page, limit.
// Returns only unacknowledged AND unresolved alerts.
// Ranking: CRITICAL → WARNING → INFO, then most recent first within each severity.
func (h *AlertHandler) HandleAttentionQueue(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	fleetID := q.Get("fleet_id")
	if fleetID == "" {
		writeError(w, http.StatusBadRequest, "fleet_id query parameter is required")
		return
	}

	ctx := r.Context()

	args := []interface{}{fleetID}
	where := `WHERE a.fleet_id = $1
	           AND a.acknowledged_at IS NULL
	           AND a.resolved_at IS NULL`

	if v := q.Get("severity"); v != "" {
		args = append(args, v)
		where += fmt.Sprintf(" AND a.severity = $%d", len(args))
	}
	if v := q.Get("alert_type"); v != "" {
		args = append(args, v)
		where += fmt.Sprintf(" AND a.alert_type = $%d", len(args))
	}

	page, limit := parsePagination(q.Get("page"), q.Get("limit"))
	offset := (page - 1) * limit

	var total int
	_ = h.tsStore.QueryRow(ctx,
		"SELECT COUNT(*) FROM vehicle_alerts a "+where, args...).Scan(&total)

	args = append(args, limit, offset)
	limitPos := len(args) - 1
	offsetPos := len(args)

	rows, err := h.tsStore.Query(ctx, `
		SELECT a.id, a.alert_type, a.severity, a.triggered_value, a.created_at,
		       a.vehicle_id, a.fleet_id,
		       COALESCE(v.display_name, a.vehicle_id) AS display_name
		FROM vehicle_alerts a
		LEFT JOIN vehicle_registry v ON v.vehicle_id = a.vehicle_id
		`+where+`
		ORDER BY
		  CASE a.severity WHEN 'CRITICAL' THEN 0 WHEN 'WARNING' THEN 1 ELSE 2 END,
		  a.created_at DESC
		LIMIT $`+fmt.Sprintf("%d", limitPos)+` OFFSET $`+fmt.Sprintf("%d", offsetPos),
		args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query alerts")
		return
	}
	defer rows.Close()

	alerts := []enrichedAlert{}
	for rows.Next() {
		var ea enrichedAlert
		var createdAt time.Time
		var alertTypeStr, severityStr string
		if e := rows.Scan(
			&ea.ID, &alertTypeStr, &severityStr, &ea.TriggerValue,
			&createdAt, &ea.VehicleID, &ea.FleetID, &ea.DisplayName,
		); e != nil {
			continue
		}
		ea.AlertType = domain.AlertType(alertTypeStr)
		ea.Severity = domain.AlertSeverity(severityStr)
		ea.CreatedAt = createdAt.Format(time.RFC3339)
		h.enrichWithTrip(ctx, &ea)
		ea.IsConditionActive = h.isConditionActive(ctx, ea.VehicleID, ea.AlertType)
		alerts = append(alerts, ea)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"fleet_id":   fleetID,
		"alerts":     alerts,
		"pagination": domain.NewPagination(page, limit, total),
	})
}

// ── Alert Detail ──────────────────────────────────────────────────────────────

// alertDetail extends enrichedAlert with fields only shown in the single-alert view.
type alertDetail struct {
	enrichedAlert
	RegistrationNumber string               `json:"registration_number"`
	VehicleType        string               `json:"vehicle_type"`
	DriverPhone        *string              `json:"driver_phone,omitempty"`
	DriverLicense      *string              `json:"driver_license,omitempty"`
	TelemetryAtAlert   *domain.VehicleState `json:"telemetry_at_alert,omitempty"`
}

// GET /api/v1/alerts/{alert_id}
func (h *AlertHandler) HandleAlertDetail(w http.ResponseWriter, r *http.Request) {
	alertID, err := strconv.ParseInt(r.PathValue("alert_id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "alert_id must be an integer")
		return
	}

	ctx := r.Context()

	var detail alertDetail
	var createdAt time.Time
	var ackAt, resolvedAt *time.Time
	var alertTypeStr, severityStr string

	err = h.tsStore.QueryRow(ctx, `
		SELECT a.id, a.alert_type, a.severity, a.triggered_value, a.created_at,
		       a.vehicle_id, a.fleet_id,
		       COALESCE(v.display_name, a.vehicle_id),
		       COALESCE(v.registration_number, ''),
		       COALESCE(v.vehicle_type, ''),
		       a.acknowledged_at, a.acknowledged_by,
		       a.resolved_at, a.resolved_by
		FROM vehicle_alerts a
		LEFT JOIN vehicle_registry v ON v.vehicle_id = a.vehicle_id
		WHERE a.id = $1
	`, alertID).Scan(
		&detail.ID, &alertTypeStr, &severityStr, &detail.TriggerValue, &createdAt,
		&detail.VehicleID, &detail.FleetID, &detail.DisplayName,
		&detail.RegistrationNumber, &detail.VehicleType,
		&ackAt, &detail.AcknowledgedBy,
		&resolvedAt, &detail.ResolvedBy,
	)
	if err != nil {
		writeError(w, http.StatusNotFound, "alert not found")
		return
	}

	detail.AlertType = domain.AlertType(alertTypeStr)
	detail.Severity = domain.AlertSeverity(severityStr)
	detail.CreatedAt = createdAt.Format(time.RFC3339)
	if ackAt != nil {
		s := ackAt.Format(time.RFC3339)
		detail.AcknowledgedAt = &s
	}
	if resolvedAt != nil {
		s := resolvedAt.Format(time.RFC3339)
		detail.ResolvedAt = &s
	}

	h.enrichWithTrip(ctx, &detail.enrichedAlert)
	detail.IsConditionActive = h.isConditionActive(ctx, detail.VehicleID, detail.AlertType)

	// Driver phone + license — only available if a trip was found by enrichWithTrip
	if detail.DriverName != nil {
		var phone, license string
		if e := h.tsStore.QueryRow(ctx, `
			SELECT d.phone_number, d.license_number
			FROM driver_registry d
			JOIN trip t ON t.driver_id = d.driver_id
			WHERE t.vehicle_id = $1 AND t.status = $2
			LIMIT 1
		`, detail.VehicleID, domain.TripInProgress).Scan(&phone, &license); e == nil {
			detail.DriverPhone = &phone
			detail.DriverLicense = &license
		}
	}

	// Telemetry snapshot closest to the moment the alert fired
	var snap domain.VehicleState
	var snapTs time.Time
	e := h.tsStore.QueryRow(ctx, `
		SELECT latitude, longitude, speed_kmh, fuel_pct,
		       engine_temp_celsius, battery_voltage, timestamp
		FROM vehicle_telemetry
		WHERE vehicle_id = $1 AND timestamp <= $2
		ORDER BY timestamp DESC
		LIMIT 1
	`, detail.VehicleID, createdAt).Scan(
		&snap.Lat, &snap.Lng, &snap.SpeedKmh, &snap.FuelPct,
		&snap.EngineTempC, &snap.Battery, &snapTs,
	)
	if e == nil {
		snap.Timestamp = snapTs.Unix()
		detail.TelemetryAtAlert = &snap
	}

	writeJSON(w, http.StatusOK, detail)
}

// ── Acknowledge Workflow ──────────────────────────────────────────────────────

type operatorBody struct {
	OperatorName string `json:"operator"`
}

// POST /api/v1/alerts/{alert_id}/acknowledge
func (h *AlertHandler) HandleAcknowledge(w http.ResponseWriter, r *http.Request) {
	alertID, err := strconv.ParseInt(r.PathValue("alert_id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "alert_id must be an integer")
		return
	}

	var body operatorBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.OperatorName == "" {
		writeError(w, http.StatusBadRequest, `request body must contain "operator" field`)
		return
	}

	ctx := r.Context()
	result, err := h.tsStore.Exec(ctx, `
		UPDATE vehicle_alerts
		SET acknowledged_at = NOW(), acknowledged_by = $1
		WHERE id = $2 AND acknowledged_at IS NULL
	`, body.OperatorName, alertID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to acknowledge alert")
		return
	}
	if result.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "alert not found or already acknowledged")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"alert_id":        alertID,
		"acknowledged_by": body.OperatorName,
		"acknowledged_at": time.Now().UTC().Format(time.RFC3339),
	})
}

// POST /api/v1/alerts/{alert_id}/resolve
func (h *AlertHandler) HandleResolve(w http.ResponseWriter, r *http.Request) {
	alertID, err := strconv.ParseInt(r.PathValue("alert_id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "alert_id must be an integer")
		return
	}

	var body operatorBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.OperatorName == "" {
		writeError(w, http.StatusBadRequest, `request body must contain "operator" field`)
		return
	}

	ctx := r.Context()
	result, err := h.tsStore.Exec(ctx, `
		UPDATE vehicle_alerts
		SET resolved_at = NOW(), resolved_by = $1
		WHERE id = $2 AND resolved_at IS NULL
	`, body.OperatorName, alertID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve alert")
		return
	}
	if result.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "alert not found or already resolved")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"alert_id":    alertID,
		"resolved_by": body.OperatorName,
		"resolved_at": time.Now().UTC().Format(time.RFC3339),
	})
}

// POST /api/v1/alerts/{alert_id}/unacknowledge
func (h *AlertHandler) HandleUnacknowledge(w http.ResponseWriter, r *http.Request) {
	alertID, err := strconv.ParseInt(r.PathValue("alert_id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "alert_id must be an integer")
		return
	}

	ctx := r.Context()
	result, err := h.tsStore.Exec(ctx, `
		UPDATE vehicle_alerts
		SET acknowledged_at = NULL, acknowledged_by = NULL
		WHERE id = $1 AND acknowledged_at IS NOT NULL
	`, alertID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to unacknowledge alert")
		return
	}
	if result.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "alert not found or not currently acknowledged")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"alert_id": alertID,
		"status":   "returned to attention queue",
	})
}

// ── Fleet Alert History ───────────────────────────────────────────────────────

// GET /api/v1/fleet/{fleet_id}/alerts
//
// Query params: from, to (RFC3339), alert_type, severity, acknowledged (bool), page, limit.
func (h *AlertHandler) HandleFleetAlertHistory(w http.ResponseWriter, r *http.Request) {
	fleetID := r.PathValue("fleet_id")
	if fleetID == "" {
		writeError(w, http.StatusBadRequest, "fleet_id path parameter is required")
		return
	}

	ctx := r.Context()
	q := r.URL.Query()

	args := []interface{}{fleetID}
	where := "WHERE a.fleet_id = $1"

	if v := q.Get("from"); v != "" {
		if t, e := time.Parse(time.RFC3339, v); e == nil {
			args = append(args, t)
			where += fmt.Sprintf(" AND a.created_at >= $%d", len(args))
		}
	}
	if v := q.Get("to"); v != "" {
		if t, e := time.Parse(time.RFC3339, v); e == nil {
			args = append(args, t)
			where += fmt.Sprintf(" AND a.created_at <= $%d", len(args))
		}
	}
	if v := q.Get("alert_type"); v != "" {
		args = append(args, v)
		where += fmt.Sprintf(" AND a.alert_type = $%d", len(args))
	}
	if v := q.Get("severity"); v != "" {
		args = append(args, v)
		where += fmt.Sprintf(" AND a.severity = $%d", len(args))
	}
	if v := q.Get("acknowledged"); v == "true" {
		where += " AND a.acknowledged_at IS NOT NULL"
	} else if v == "false" {
		where += " AND a.acknowledged_at IS NULL"
	}

	page, limit := parsePagination(q.Get("page"), q.Get("limit"))
	offset := (page - 1) * limit

	var total int
	_ = h.tsStore.QueryRow(ctx,
		"SELECT COUNT(*) FROM vehicle_alerts a "+where, args...).Scan(&total)

	args = append(args, limit, offset)
	limitPos := len(args) - 1
	offsetPos := len(args)

	rows, err := h.tsStore.Query(ctx, `
		SELECT a.id, a.alert_type, a.severity, a.triggered_value, a.created_at,
		       a.vehicle_id, a.fleet_id,
		       COALESCE(v.display_name, a.vehicle_id),
		       a.acknowledged_at, a.acknowledged_by,
		       a.resolved_at, a.resolved_by
		FROM vehicle_alerts a
		LEFT JOIN vehicle_registry v ON v.vehicle_id = a.vehicle_id
		`+where+`
		ORDER BY a.created_at DESC
		LIMIT $`+fmt.Sprintf("%d", limitPos)+` OFFSET $`+fmt.Sprintf("%d", offsetPos),
		args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query fleet alerts")
		return
	}
	defer rows.Close()

	type historyRow struct {
		domain.Alert
		DisplayName string `json:"display_name"`
	}

	alerts := []historyRow{}
	for rows.Next() {
		var hr historyRow
		var createdAt time.Time
		var ackAt, resolvedAt *time.Time
		var alertTypeStr, severityStr string
		if e := rows.Scan(
			&hr.ID, &alertTypeStr, &severityStr, &hr.TriggerValue,
			&createdAt, &hr.VehicleID, &hr.FleetID, &hr.DisplayName,
			&ackAt, &hr.AcknowledgedBy, &resolvedAt, &hr.ResolvedBy,
		); e != nil {
			continue
		}
		hr.AlertType = domain.AlertType(alertTypeStr)
		hr.Severity = domain.AlertSeverity(severityStr)
		hr.CreatedAt = createdAt.Format(time.RFC3339)
		if ackAt != nil {
			s := ackAt.Format(time.RFC3339)
			hr.AcknowledgedAt = &s
		}
		if resolvedAt != nil {
			s := resolvedAt.Format(time.RFC3339)
			hr.ResolvedAt = &s
		}
		alerts = append(alerts, hr)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"fleet_id":   fleetID,
		"alerts":     alerts,
		"pagination": domain.NewPagination(page, limit, total),
	})
}

// ── internal methods ──────────────────────────────────────────────────────────

// enrichWithTrip looks up the active trip for an alert's vehicle and populates
// DriverName and RouteName on the enrichedAlert. Best-effort — no-op on any error.
func (h *AlertHandler) enrichWithTrip(ctx context.Context, ea *enrichedAlert) {
	var driverName, routeName string
	err := h.tsStore.QueryRow(ctx, `
		SELECT d.full_name, r.route_name
		FROM trip t
		JOIN driver_registry d ON d.driver_id = t.driver_id
		JOIN route_registry r  ON r.route_id  = t.route_id
		WHERE t.vehicle_id = $1 AND t.status = $2
		LIMIT 1
	`, ea.VehicleID, domain.TripInProgress).Scan(&driverName, &routeName)
	if err == nil {
		ea.DriverName = &driverName
		ea.RouteName = &routeName
	}
}

// isConditionActive cross-references the latest Redis state to check whether
// the condition that triggered the alert is still present right now.
// Uses parseFloat from helpers.go.
func (h *AlertHandler) isConditionActive(ctx context.Context, vehicleID string, alertType domain.AlertType) bool {
	stateKey := fmt.Sprintf("vehicle:%s:state", vehicleID)
	vals, err := h.redis.HMGet(ctx, stateKey, "speed_kmh", "fuel_pct", "engine_temp").Result()
	if err != nil || len(vals) < 3 {
		return false
	}

	switch alertType {
	case domain.AlertSpeeding:
		return parseFloat(vals[0]) > 100.0
	case domain.AlertLowFuel:
		return parseFloat(vals[1]) < 10.0
	case domain.AlertEngineOverheat:
		return parseFloat(vals[2]) > 100.0
	case domain.AlertRouteDeviation:
		devKey := fmt.Sprintf("vehicle:%s:deviation", vehicleID)
		v, _ := h.redis.Get(ctx, devKey).Result()
		return v == "true"
	}
	return false
}
