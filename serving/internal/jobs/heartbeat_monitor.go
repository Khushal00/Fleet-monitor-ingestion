package jobs

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"fleet-monitor/serving/internal/ws"
)

type HeartbeatMonitor struct {
	redis            *redis.Client
	db               *pgxpool.Pool
	hub              *ws.Hub
	interval         time.Duration
	defaultThreshold time.Duration
}

func NewHeartbeatMonitor(rc *redis.Client, db *pgxpool.Pool, hub *ws.Hub, intervalSec, thresholdSec int) *HeartbeatMonitor {
	return &HeartbeatMonitor{
		redis:            rc,
		db:               db,
		hub:              hub,
		interval:         time.Duration(intervalSec) * time.Second,
		defaultThreshold: time.Duration(thresholdSec) * time.Second,
	}
}

func (h *HeartbeatMonitor) Run(ctx context.Context) {
	log.Printf("heartbeat: started (interval=%s)", h.interval)
	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()
	h.tick(ctx)
	for {
		select {
		case <-ticker.C:
			h.tick(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (h *HeartbeatMonitor) tick(ctx context.Context) {
	thresholds, _ := h.loadFleetThresholds(ctx)

	rows, err := h.db.Query(ctx, `SELECT vehicle_id, fleet_id FROM vehicle_registry WHERE active = true`)
	if err != nil {
		log.Printf("heartbeat: load vehicles: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var vehicleID, fleetID string
		if err := rows.Scan(&vehicleID, &fleetID); err != nil {
			continue
		}
		threshold := h.defaultThreshold
		if t, ok := thresholds[fleetID]; ok {
			threshold = t
		}
		h.check(ctx, vehicleID, fleetID, threshold)
	}
}

func (h *HeartbeatMonitor) check(ctx context.Context, vehicleID, fleetID string, threshold time.Duration) {
	stateKey := fmt.Sprintf("vehicle:%s:state", vehicleID)
	onlineKey := fmt.Sprintf("vehicle:%s:online_status", vehicleID)

	recvStr, err := h.redis.HGet(ctx, stateKey, "received_at").Result()
	if err == redis.Nil {
		h.setOffline(ctx, vehicleID, fleetID, onlineKey, time.Time{})
		return
	}
	if err != nil {
		return
	}

	recvUnix, err := strconv.ParseInt(recvStr, 10, 64)
	if err != nil {
		return
	}

	current, _ := h.redis.Get(ctx, onlineKey).Result()

	if time.Since(time.Unix(recvUnix, 0)) > threshold {
		h.setOffline(ctx, vehicleID, fleetID, onlineKey, time.Unix(recvUnix, 0))
	} else if current != "online" {
		h.redis.Set(ctx, onlineKey, "online", 0)
		log.Printf("heartbeat: %s online", vehicleID)
		h.hub.BroadcastOnline(fleetID, ws.VehicleOnlinePayload{VehicleID: vehicleID})
	}
}

func (h *HeartbeatMonitor) setOffline(ctx context.Context, vehicleID, fleetID, onlineKey string, lastSeen time.Time) {
	current, _ := h.redis.Get(ctx, onlineKey).Result()
	if current == "offline" {
		return
	}
	h.redis.Set(ctx, onlineKey, "offline", 0)
	log.Printf("heartbeat: %s offline", vehicleID)
	h.hub.BroadcastOffline(fleetID, ws.VehicleOfflinePayload{VehicleID: vehicleID, LastSeenAt: lastSeen})
}

func (h *HeartbeatMonitor) loadFleetThresholds(ctx context.Context) (map[string]time.Duration, error) {
	rows, err := h.db.Query(ctx, `SELECT fleet_id, staleness_threshold_seconds FROM fleet_config`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]time.Duration)
	for rows.Next() {
		var fleetID string
		var secs int
		if rows.Scan(&fleetID, &secs) == nil {
			out[fleetID] = time.Duration(secs) * time.Second
		}
	}
	return out, rows.Err()
}
