package jobs

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type ETAEstimator struct {
	redis    *redis.Client
	db       *pgxpool.Pool
	interval time.Duration
}

func NewETAEstimator(rc *redis.Client, db *pgxpool.Pool, intervalSec int) *ETAEstimator {
	return &ETAEstimator{
		redis:    rc,
		db:       db,
		interval: time.Duration(intervalSec) * time.Second,
	}
}

func (e *ETAEstimator) Run(ctx context.Context) {
	log.Printf("eta-estimator: started (interval=%s)", e.interval)
	ticker := time.NewTicker(e.interval)
	defer ticker.Stop()
	e.tick(ctx)
	for {
		select {
		case <-ticker.C:
			e.tick(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (e *ETAEstimator) tick(ctx context.Context) {
	rows, err := e.db.Query(ctx, `
		SELECT t.trip_id, t.vehicle_id, rs.lat, rs.lng
		FROM trip t
		JOIN route_stops rs ON rs.route_id = t.route_id
		LEFT JOIN trip_stop_progress tsp
		       ON tsp.trip_id = t.trip_id AND tsp.stop_id = rs.stop_id
		WHERE t.status = 'IN_PROGRESS'
		  AND COALESCE(tsp.status, 'PENDING') = 'PENDING'
		ORDER BY t.trip_id, rs.stop_sequence
	`)
	if err != nil {
		log.Printf("eta-estimator: query: %v", err)
		return
	}
	defer rows.Close()

	type nextStop struct {
		vehicleID string
		lat       float64
		lng       float64
	}
	next := make(map[string]nextStop) // tripID → first pending stop

	for rows.Next() {
		var tripID, vehicleID string
		var lat, lng float64
		if rows.Scan(&tripID, &vehicleID, &lat, &lng) != nil {
			continue
		}
		if _, seen := next[tripID]; !seen {
			next[tripID] = nextStop{vehicleID: vehicleID, lat: lat, lng: lng}
		}
	}

	for tripID, ns := range next {
		e.computeAndStore(ctx, tripID, ns.vehicleID, ns.lat, ns.lng)
	}
}

func (e *ETAEstimator) computeAndStore(ctx context.Context, tripID, vehicleID string, destLat, destLng float64) {
	stateKey := fmt.Sprintf("vehicle:%s:state", vehicleID)
	vals, err := e.redis.HMGet(ctx, stateKey, "lat", "lng", "speed_kmh").Result()
	if err != nil || vals[0] == nil || vals[1] == nil {
		return
	}

	vLat, err1 := strconv.ParseFloat(fmt.Sprintf("%v", vals[0]), 64)
	vLng, err2 := strconv.ParseFloat(fmt.Sprintf("%v", vals[1]), 64)
	if err1 != nil || err2 != nil {
		return
	}

	speedKmh := 60.0 // default
	if vals[2] != nil {
		if s, err := strconv.ParseFloat(fmt.Sprintf("%v", vals[2]), 64); err == nil && s > 5 {
			speedKmh = s
		}
	}

	distKm := haversineKm(vLat, vLng, destLat, destLng)
	etaMinutes := (distKm / speedKmh) * 60
	eta := time.Now().UTC().Add(time.Duration(etaMinutes) * time.Minute)

	etaKey := fmt.Sprintf("trip:%s:eta", tripID)
	if err := e.redis.Set(ctx, etaKey, eta.Format(time.RFC3339), 10*time.Minute).Err(); err != nil {
		log.Printf("eta-estimator: store eta for trip %s: %v", tripID, err)
	}
}
