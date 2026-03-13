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

type DeviationDetector struct {
	redis    *redis.Client
	db       *pgxpool.Pool
	hub      *ws.Hub
	interval time.Duration
}

func NewDeviationDetector(rc *redis.Client, db *pgxpool.Pool, hub *ws.Hub, intervalSec int) *DeviationDetector {
	return &DeviationDetector{
		redis:    rc,
		db:       db,
		hub:      hub,
		interval: time.Duration(intervalSec) * time.Second,
	}
}

func (d *DeviationDetector) Run(ctx context.Context) {
	log.Printf("deviation: started (interval=%s)", d.interval)
	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()
	d.tick(ctx)
	for {
		select {
		case <-ticker.C:
			d.tick(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (d *DeviationDetector) tick(ctx context.Context) {
	trips, err := d.loadActiveTrips(ctx)
	if err != nil {
		log.Printf("deviation: load trips: %v", err)
		return
	}
	for _, t := range trips {
		d.checkTrip(ctx, t)
	}
}

type activeTrip struct {
	tripID           string
	vehicleID        string
	fleetID          string
	corridorRadiusKm float64
	stops            []stopPoint
}

type stopPoint struct {
	lat float64
	lng float64
}

func (d *DeviationDetector) checkTrip(ctx context.Context, t activeTrip) {
	stateKey := fmt.Sprintf("vehicle:%s:state", t.vehicleID)
	vals, err := d.redis.HMGet(ctx, stateKey, "lat", "lng").Result()
	if err != nil || vals[0] == nil || vals[1] == nil {
		return
	}
	lat, err1 := strconv.ParseFloat(fmt.Sprintf("%v", vals[0]), 64)
	lng, err2 := strconv.ParseFloat(fmt.Sprintf("%v", vals[1]), 64)
	if err1 != nil || err2 != nil {
		return
	}

	minDist := d.minDistanceToRoute(lat, lng, t.stops)
	devKey := fmt.Sprintf("vehicle:%s:deviation", t.vehicleID)

	if minDist > t.corridorRadiusKm {
		d.redis.Set(ctx, devKey, "true", 0)
		d.fireAlert(ctx, t, lat, lng, minDist)
		d.hub.BroadcastDeviation(t.fleetID, ws.VehicleDeviationPayload{
			VehicleID:   t.vehicleID,
			TripID:      t.tripID,
			DeviationKm: minDist,
		})
	} else {
		d.redis.Set(ctx, devKey, "false", 0)
		d.autoResolve(ctx, t.vehicleID, t.tripID)
	}
}

func (d *DeviationDetector) minDistanceToRoute(lat, lng float64, stops []stopPoint) float64 {
	if len(stops) == 0 {
		return 0
	}
	if len(stops) == 1 {
		return haversineKm(lat, lng, stops[0].lat, stops[0].lng)
	}
	min := 1e18
	for i := 0; i < len(stops)-1; i++ {
		dist := pointToSegmentDistanceKm(lat, lng,
			stops[i].lat, stops[i].lng,
			stops[i+1].lat, stops[i+1].lng)
		if dist < min {
			min = dist
		}
	}
	return min
}

func (d *DeviationDetector) fireAlert(ctx context.Context, t activeTrip, lat, lng, deviationKm float64) {
	dedupKey := fmt.Sprintf("deviation:%s:%s", t.vehicleID, t.tripID)
	if set, _ := d.redis.SetNX(ctx, dedupKey, "1", 10*time.Minute).Result(); !set {
		return
	}

	severity := "WARNING"
	if deviationKm > t.corridorRadiusKm*2 {
		severity = "CRITICAL"
	}

	_, err := d.db.Exec(ctx, `
		INSERT INTO vehicle_alerts (vehicle_id, fleet_id, alert_type, severity, triggered_value, created_at)
		SELECT $1, fleet_id, 'ROUTE_DEVIATION', $2, $3, NOW()
		FROM vehicle_registry WHERE vehicle_id = $1
	`, t.vehicleID, severity, deviationKm)
	if err != nil {
		log.Printf("deviation: insert alert for %s: %v", t.vehicleID, err)
	}
}

func (d *DeviationDetector) autoResolve(ctx context.Context, vehicleID, tripID string) {
	_, err := d.db.Exec(ctx, `
		UPDATE vehicle_alerts
		SET    resolved_at = NOW(), resolved_by = 'system'
		WHERE  vehicle_id  = $1
		  AND  alert_type  = 'ROUTE_DEVIATION'
		  AND  resolved_at IS NULL
	`, vehicleID)
	if err != nil {
		log.Printf("deviation: auto-resolve for %s: %v", vehicleID, err)
		return
	}
	d.redis.Del(ctx, fmt.Sprintf("deviation:%s:%s", vehicleID, tripID))
}

func (d *DeviationDetector) loadActiveTrips(ctx context.Context) ([]activeTrip, error) {
	rows, err := d.db.Query(ctx, `
		SELECT t.trip_id, t.vehicle_id, vr.fleet_id, r.corridor_radius_km
		FROM trip t
		JOIN vehicle_registry vr ON vr.vehicle_id = t.vehicle_id
		JOIN route_registry r    ON r.route_id    = t.route_id
		WHERE t.status = 'IN_PROGRESS'
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var trips []activeTrip
	for rows.Next() {
		var at activeTrip
		if err := rows.Scan(&at.tripID, &at.vehicleID, &at.fleetID, &at.corridorRadiusKm); err != nil {
			continue
		}
		stops, err := d.loadRouteStops(ctx, at.tripID)
		if err != nil {
			log.Printf("deviation: load stops for trip %s: %v", at.tripID, err)
			continue
		}
		at.stops = stops
		trips = append(trips, at)
	}
	return trips, rows.Err()
}

func (d *DeviationDetector) loadRouteStops(ctx context.Context, tripID string) ([]stopPoint, error) {
	rows, err := d.db.Query(ctx, `
		SELECT rs.lat, rs.lng
		FROM route_stops rs
		JOIN trip t ON t.route_id = rs.route_id
		WHERE t.trip_id = $1
		ORDER BY rs.stop_sequence
	`, tripID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var stops []stopPoint
	for rows.Next() {
		var s stopPoint
		if rows.Scan(&s.lat, &s.lng) == nil {
			stops = append(stops, s)
		}
	}
	return stops, rows.Err()
}
