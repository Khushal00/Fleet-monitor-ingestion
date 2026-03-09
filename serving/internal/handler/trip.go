package handler

import (
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"fleet-monitor/serving/internal/domain"
)

// TripHandler serves trip read endpoints:
//
//	GET /api/v1/trips            — list trips (filter by fleet, status, date)
//	GET /api/v1/trips/{trip_id}  — full trip detail with stops and ETA
type TripHandler struct {
	redis   *redis.Client
	tsStore *pgxpool.Pool
}

func NewTripHandler(redisClient *redis.Client, tsStore *pgxpool.Pool) *TripHandler {
	return &TripHandler{
		redis:   redisClient,
		tsStore: tsStore,
	}
}

// ── Response types ────────────────────────────────────────────────────────────

type tripListRow struct {
	TripID             string            `json:"trip_id"`
	VehicleID          string            `json:"vehicle_id"`
	VehicleName        string            `json:"vehicle_name"`
	DriverID           string            `json:"driver_id"`
	DriverName         string            `json:"driver_name"`
	RouteID            string            `json:"route_id"`
	RouteName          string            `json:"route_name"`
	FleetID            string            `json:"fleet_id"`
	Status             domain.TripStatus `json:"status"`
	ScheduledDeparture string            `json:"scheduled_departure"`
	ActualDeparture    *string           `json:"actual_departure,omitempty"`
	CompletedAt        *string           `json:"completed_at,omitempty"`
	CreatedAt          string            `json:"created_at"`
}

type tripDetail struct {
	TripID             string                `json:"trip_id"`
	Status             domain.TripStatus     `json:"status"`
	ScheduledDeparture string                `json:"scheduled_departure"`
	ActualDeparture    *string               `json:"actual_departure,omitempty"`
	CompletedAt        *string               `json:"completed_at,omitempty"`
	CreatedAt          string                `json:"created_at"`
	Vehicle            tripVehicle           `json:"vehicle"`
	Driver             tripDriver            `json:"driver"`
	Route              tripRoute             `json:"route"`
	Stops              []domain.StopProgress `json:"stops"`
	StopsTotal         int                   `json:"stops_total"`
	StopsCompleted     int                   `json:"stops_completed"`
	ETA                *string               `json:"eta,omitempty"`
	DeviationStatus    string                `json:"deviation_status"` // "on_route" | "deviated"
}

type tripVehicle struct {
	VehicleID          string `json:"vehicle_id"`
	DisplayName        string `json:"display_name"`
	RegistrationNumber string `json:"registration_number"`
	FleetID            string `json:"fleet_id"`
}

type tripDriver struct {
	DriverID    string `json:"driver_id"`
	FullName    string `json:"full_name"`
	PhoneNumber string `json:"phone_number"`
}

type tripRoute struct {
	RouteID         string  `json:"route_id"`
	RouteName       string  `json:"route_name"`
	OriginName      string  `json:"origin_name"`
	DestinationName string  `json:"destination_name"`
	TotalDistanceKm float64 `json:"total_distance_km"`
}

// ── List trips ────────────────────────────────────────────────────────────────

// GET /api/v1/trips
//
// Query params: fleet_id, status, from, to (scheduled_departure range), page, limit.
func (h *TripHandler) HandleList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()

	args := []interface{}{}
	where := "WHERE 1=1"

	if v := q.Get("fleet_id"); v != "" {
		args = append(args, v)
		where += fmt.Sprintf(" AND v.fleet_id = $%d", len(args))
	}
	if v := q.Get("status"); v != "" {
		args = append(args, v)
		where += fmt.Sprintf(" AND t.status = $%d", len(args))
	}
	if v := q.Get("from"); v != "" {
		if ts, e := time.Parse(time.RFC3339, v); e == nil {
			args = append(args, ts)
			where += fmt.Sprintf(" AND t.scheduled_departure >= $%d", len(args))
		}
	}
	if v := q.Get("to"); v != "" {
		if ts, e := time.Parse(time.RFC3339, v); e == nil {
			args = append(args, ts)
			where += fmt.Sprintf(" AND t.scheduled_departure <= $%d", len(args))
		}
	}

	page, limit := parsePagination(q.Get("page"), q.Get("limit"))
	offset := (page - 1) * limit

	var total int
	_ = h.tsStore.QueryRow(ctx, `
		SELECT COUNT(*) FROM trip t
		JOIN vehicle_registry v ON v.vehicle_id = t.vehicle_id
		`+where, args...).Scan(&total)

	args = append(args, limit, offset)
	limitPos := len(args) - 1
	offsetPos := len(args)

	rows, err := h.tsStore.Query(ctx, `
		SELECT t.trip_id, t.vehicle_id, t.driver_id, t.route_id,
		       t.status, t.scheduled_departure, t.actual_departure,
		       t.completed_at, t.created_at,
		       COALESCE(v.display_name, t.vehicle_id),
		       COALESCE(d.full_name, t.driver_id),
		       COALESCE(r.route_name, t.route_id),
		       v.fleet_id
		FROM trip t
		JOIN vehicle_registry v  ON v.vehicle_id = t.vehicle_id
		LEFT JOIN driver_registry d ON d.driver_id = t.driver_id
		LEFT JOIN route_registry r  ON r.route_id  = t.route_id
		`+where+`
		ORDER BY t.scheduled_departure DESC
		LIMIT $`+fmt.Sprintf("%d", limitPos)+` OFFSET $`+fmt.Sprintf("%d", offsetPos),
		args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query trips")
		return
	}
	defer rows.Close()

	trips := []tripListRow{}
	for rows.Next() {
		var t tripListRow
		var scheduledDep, createdAt time.Time
		var actualDep, completedAt *time.Time
		var statusStr string
		if e := rows.Scan(
			&t.TripID, &t.VehicleID, &t.DriverID, &t.RouteID,
			&statusStr, &scheduledDep, &actualDep, &completedAt, &createdAt,
			&t.VehicleName, &t.DriverName, &t.RouteName, &t.FleetID,
		); e != nil {
			continue
		}
		t.Status = domain.TripStatus(statusStr)
		t.ScheduledDeparture = scheduledDep.Format(time.RFC3339)
		t.CreatedAt = createdAt.Format(time.RFC3339)
		if actualDep != nil {
			s := actualDep.Format(time.RFC3339)
			t.ActualDeparture = &s
		}
		if completedAt != nil {
			s := completedAt.Format(time.RFC3339)
			t.CompletedAt = &s
		}
		trips = append(trips, t)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"trips":      trips,
		"pagination": domain.NewPagination(page, limit, total),
	})
}

// ── Trip detail ───────────────────────────────────────────────────────────────

// GET /api/v1/trips/{trip_id}
func (h *TripHandler) HandleDetail(w http.ResponseWriter, r *http.Request) {
	tripID := r.PathValue("trip_id")
	if tripID == "" {
		writeError(w, http.StatusBadRequest, "trip_id path parameter is required")
		return
	}

	ctx := r.Context()

	var td tripDetail
	var scheduledDep, createdAt time.Time
	var actualDep, completedAt *time.Time
	var routeID, statusStr string

	err := h.tsStore.QueryRow(ctx, `
		SELECT t.trip_id, t.status, t.scheduled_departure, t.actual_departure,
		       t.completed_at, t.created_at, t.route_id,
		       v.vehicle_id, v.display_name, v.registration_number, v.fleet_id,
		       d.driver_id, d.full_name, d.phone_number,
		       r.route_id, r.route_name, r.origin_name, r.destination_name,
		       COALESCE(r.total_distance_km, 0)
		FROM trip t
		JOIN vehicle_registry v ON v.vehicle_id = t.vehicle_id
		JOIN driver_registry d  ON d.driver_id  = t.driver_id
		JOIN route_registry r   ON r.route_id   = t.route_id
		WHERE t.trip_id = $1
	`, tripID).Scan(
		&td.TripID, &statusStr, &scheduledDep, &actualDep,
		&completedAt, &createdAt, &routeID,
		&td.Vehicle.VehicleID, &td.Vehicle.DisplayName,
		&td.Vehicle.RegistrationNumber, &td.Vehicle.FleetID,
		&td.Driver.DriverID, &td.Driver.FullName, &td.Driver.PhoneNumber,
		&td.Route.RouteID, &td.Route.RouteName, &td.Route.OriginName,
		&td.Route.DestinationName, &td.Route.TotalDistanceKm,
	)
	if err != nil {
		writeError(w, http.StatusNotFound, "trip not found")
		return
	}

	td.Status = domain.TripStatus(statusStr)
	td.ScheduledDeparture = scheduledDep.Format(time.RFC3339)
	td.CreatedAt = createdAt.Format(time.RFC3339)
	if actualDep != nil {
		s := actualDep.Format(time.RFC3339)
		td.ActualDeparture = &s
	}
	if completedAt != nil {
		s := completedAt.Format(time.RFC3339)
		td.CompletedAt = &s
	}

	// Stop progress — LEFT JOIN so stops without progress rows default to PENDING
	stopRows, err := h.tsStore.Query(ctx, `
		SELECT rs.stop_id, rs.stop_name, rs.stop_sequence,
		       COALESCE(tsp.status, $1),
		       tsp.arrived_at, tsp.departed_at
		FROM route_stops rs
		LEFT JOIN trip_stop_progress tsp
		       ON tsp.stop_id = rs.stop_id AND tsp.trip_id = $2
		WHERE rs.route_id = $3
		ORDER BY rs.stop_sequence
	`, string(domain.StopPending), tripID, routeID)
	if err == nil {
		defer stopRows.Close()
		for stopRows.Next() {
			var sp domain.StopProgress
			var arrivedAt, departedAt *time.Time
			var statusStr string
			if e := stopRows.Scan(
				&sp.StopID, &sp.StopName, &sp.Sequence,
				&statusStr, &arrivedAt, &departedAt,
			); e == nil {
				sp.Status = domain.StopStatus(statusStr)
				if arrivedAt != nil {
					s := arrivedAt.Format(time.RFC3339)
					sp.ArrivedAt = &s
				}
				if departedAt != nil {
					s := departedAt.Format(time.RFC3339)
					sp.DepartedAt = &s
				}
				td.Stops = append(td.Stops, sp)
				td.StopsTotal++
				if sp.Status == domain.StopArrived || sp.Status == domain.StopDeparted {
					td.StopsCompleted++
				}
			}
		}
	}
	if td.Stops == nil {
		td.Stops = []domain.StopProgress{}
	}

	// ETA and deviation from Redis
	if etaVal, e := h.redis.Get(ctx, fmt.Sprintf("trip:%s:eta", tripID)).Result(); e == nil {
		td.ETA = &etaVal
	}

	if devVal, _ := h.redis.Get(ctx, fmt.Sprintf("vehicle:%s:deviation", td.Vehicle.VehicleID)).Result(); devVal == "true" {
		td.DeviationStatus = "deviated"
	} else {
		td.DeviationStatus = "on_route"
	}

	writeJSON(w, http.StatusOK, td)
}
