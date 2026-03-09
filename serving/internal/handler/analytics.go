package handler

import (
	"context"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"fleet-monitor/serving/internal/domain"
)

// AnalyticsHandler serves the live summary numbers shown at the top of the
// operations dashboard. Each metric answers one operational question.
// Source mix: Redis (live state), TimescaleDB (alerts, registry, trips — all one DB).
type AnalyticsHandler struct {
	redis   *redis.Client
	tsStore *pgxpool.Pool // single TimescaleDB pool — all tables live here
}

func NewAnalyticsHandler(redisClient *redis.Client, tsStore *pgxpool.Pool) *AnalyticsHandler {
	return &AnalyticsHandler{
		redis:   redisClient,
		tsStore: tsStore,
	}
}

// AnalyticsSummaryResponse is the full summary payload returned to the dashboard.
// Refreshed every 30 seconds by the client, or triggered by a WebSocket event.
type AnalyticsSummaryResponse struct {
	FleetID         string `json:"fleet_id"`
	TotalVehicles   int    `json:"total_vehicles"` // total registered active vehicles in vehicle_registry
	LiveVehicles    int    `json:"live_vehicles"`  // vehicles currently sending telemetry (have a Redis state hash)
	ActiveMoving    int    `json:"active_moving"`  // live + engine on + is_moving = true
	Idle            int    `json:"idle"`           // live + engine on + is_moving = false
	Parked          int    `json:"parked"`         // live + engine off + is_moving = false
	Offline         int    `json:"offline"`        // total_vehicles - live_vehicles
	OpenAlerts      int    `json:"open_alerts"`    // unacknowledged and unresolved
	TripsInProgress int    `json:"trips_in_progress"`
}

// GET /api/v1/fleet/{fleet_id}/analytics
func (h *AnalyticsHandler) HandleSummary(w http.ResponseWriter, r *http.Request) {
	fleetID := r.PathValue("fleet_id")
	if fleetID == "" {
		writeError(w, http.StatusBadRequest, "fleet_id path parameter is required")
		return
	}

	ctx := r.Context()

	// ── 1. Total registered vehicles ────────────────────────────────────────
	// Ground truth count from vehicle_registry — all active vehicles for this fleet.
	// Used to compute offline = total_vehicles - live_vehicles.
	var totalVehicles int
	err := h.tsStore.QueryRow(ctx, `
		SELECT COUNT(*) FROM vehicle_registry
		WHERE fleet_id = $1 AND active = true
	`, fleetID).Scan(&totalVehicles)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count vehicles")
		return
	}

	// ── 2. Live state counts from Redis ─────────────────────────────────────
	// Scan all vehicle:{id}:state hashes for this fleet.
	// liveCount = vehicles that have an active Redis state hash right now,
	// meaning they are actively sending telemetry.
	activeMoving, idle, parked, liveCount, err := h.countLiveStates(ctx, fleetID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read live vehicle states")
		return
	}

	// Offline = registered vehicles not represented in any live Redis state hash.
	// Guard against negative value in case of a race between DB count and Redis scan.
	offline := totalVehicles - liveCount
	if offline < 0 {
		offline = 0
	}

	// ── 3. Open alerts ───────────────────────────────────────────────────────
	// Unacknowledged AND unresolved — both conditions must hold for an alert
	// to be considered open and appear in the attention queue.
	var openAlerts int
	err = h.tsStore.QueryRow(ctx, `
		SELECT COUNT(*) FROM vehicle_alerts
		WHERE fleet_id = $1
		  AND acknowledged_at IS NULL
		  AND resolved_at IS NULL
	`, fleetID).Scan(&openAlerts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count open alerts")
		return
	}

	// ── 4. Trips in progress ─────────────────────────────────────────────────
	// trip does not have a fleet_id column — join through vehicle_registry
	// to filter by fleet.
	var tripsInProgress int
	err = h.tsStore.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM trip t
		JOIN vehicle_registry v ON v.vehicle_id = t.vehicle_id
		WHERE v.fleet_id = $1 AND t.status = $2
	`, fleetID, domain.TripInProgress).Scan(&tripsInProgress)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count in-progress trips")
		return
	}

	resp := AnalyticsSummaryResponse{
		FleetID:         fleetID,
		TotalVehicles:   totalVehicles,
		LiveVehicles:    liveCount,
		ActiveMoving:    activeMoving,
		Idle:            idle,
		Parked:          parked,
		Offline:         offline,
		OpenAlerts:      openAlerts,
		TripsInProgress: tripsInProgress,
	}

	writeJSON(w, http.StatusOK, resp)
}

// countLiveStates scans all vehicle:{id}:state hashes that belong to the given fleet.
// Returns (activeMoving, idle, parked, liveCount, error).
//
// Design note: we SCAN for all state keys then HMGET each. This is O(n) on vehicle
// count but fleets are bounded (hundreds, not millions). For larger deployments,
// maintain a fleet:{id}:vehicles SET in Redis and use SMEMBERS to avoid SCAN.
func (h *AnalyticsHandler) countLiveStates(ctx context.Context, fleetID string) (moving, idle, parked, total int, err error) {
	pattern := "vehicle:*:state"
	var cursor uint64

	for {
		var keys []string
		keys, cursor, err = h.redis.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return
		}

		for _, key := range keys {
			vals, hErr := h.redis.HMGet(ctx, key, "fleet_id", "is_moving", "engine_on").Result()
			if hErr != nil {
				continue
			}
			// Skip hashes from other fleets or with missing fields.
			if len(vals) < 3 || vals[0] == nil {
				continue
			}
			if fmt.Sprintf("%v", vals[0]) != fleetID {
				continue
			}

			isMoving := parseBool(vals[1])
			engineOn := parseBool(vals[2])

			total++
			switch {
			case isMoving:
				moving++
			case engineOn && !isMoving:
				idle++
			default:
				parked++
			}
		}

		if cursor == 0 {
			break
		}
	}
	return
}
