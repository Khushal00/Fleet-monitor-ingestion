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

type StopDetector struct {
	redis    *redis.Client
	db       *pgxpool.Pool
	hub      *ws.Hub
	interval time.Duration
}

func NewStopDetector(rc *redis.Client, db *pgxpool.Pool, hub *ws.Hub, intervalSec int) *StopDetector {
	return &StopDetector{
		redis:    rc,
		db:       db,
		hub:      hub,
		interval: time.Duration(intervalSec) * time.Second,
	}
}

func (s *StopDetector) Run(ctx context.Context) {
	log.Printf("stop-detector: started (interval=%s)", s.interval)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	s.tick(ctx)
	for {
		select {
		case <-ticker.C:
			s.tick(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (s *StopDetector) tick(ctx context.Context) {
	trips, err := s.loadActiveTrips(ctx)
	if err != nil {
		log.Printf("stop-detector: load trips: %v", err)
		return
	}
	for _, t := range trips {
		s.processTrip(ctx, t)
	}
}

type tripStopRow struct {
	tripID          string
	vehicleID       string
	fleetID         string
	stopID          string
	stopName        string
	stopSequence    int
	stopLat         float64
	stopLng         float64
	arrivalRadiusKm float64
	status          string
	isFinalStop     bool
}

func (s *StopDetector) processTrip(ctx context.Context, tripID string) {
	stops, err := s.loadTripStops(ctx, tripID)
	if err != nil || len(stops) == 0 {
		return
	}

	vehicleID := stops[0].vehicleID
	fleetID := stops[0].fleetID

	stateKey := fmt.Sprintf("vehicle:%s:state", vehicleID)
	vals, err := s.redis.HMGet(ctx, stateKey, "lat", "lng").Result()
	if err != nil || vals[0] == nil || vals[1] == nil {
		return
	}
	lat, err1 := strconv.ParseFloat(fmt.Sprintf("%v", vals[0]), 64)
	lng, err2 := strconv.ParseFloat(fmt.Sprintf("%v", vals[1]), 64)
	if err1 != nil || err2 != nil {
		return
	}

	// Find max sequence that has been ARRIVED/DEPARTED to detect missed stops.
	maxArrivedSeq := 0
	for _, st := range stops {
		if (st.status == "ARRIVED" || st.status == "DEPARTED") && st.stopSequence > maxArrivedSeq {
			maxArrivedSeq = st.stopSequence
		}
	}

	for i, st := range stops {
		dist := haversineKm(lat, lng, st.stopLat, st.stopLng)

		switch st.status {
		case "PENDING":
			if st.stopSequence < maxArrivedSeq {
				s.markStop(ctx, st.tripID, st.stopID, "MISSED", nil, nil)
				continue
			}
			if dist <= st.arrivalRadiusKm {
				now := time.Now().UTC()
				s.markStop(ctx, st.tripID, st.stopID, "ARRIVED", &now, nil)
				s.hub.BroadcastStopArrived(fleetID, ws.StopArrivedPayload{
					VehicleID: vehicleID,
					TripID:    st.tripID,
					StopID:    st.stopID,
					StopName:  st.stopName,
					ArrivedAt: now,
				})
				if st.isFinalStop {
					s.completeTrip(ctx, st.tripID)
				}
			}

		case "ARRIVED":
			if dist > st.arrivalRadiusKm {
				now := time.Now().UTC()
				s.markStop(ctx, st.tripID, st.stopID, "DEPARTED", nil, &now)
			}
			_ = i
		}
	}
}

func (s *StopDetector) markStop(ctx context.Context, tripID, stopID, status string, arrivedAt, departedAt *time.Time) {
	_, err := s.db.Exec(ctx, `
		UPDATE trip_stop_progress
		SET    status      = $1,
		       arrived_at  = COALESCE($2, arrived_at),
		       departed_at = COALESCE($3, departed_at)
		WHERE  trip_id = $4 AND stop_id = $5
	`, status, arrivedAt, departedAt, tripID, stopID)
	if err != nil {
		log.Printf("stop-detector: markStop %s/%s → %s: %v", tripID, stopID, status, err)
	}
}

func (s *StopDetector) completeTrip(ctx context.Context, tripID string) {
	_, err := s.db.Exec(ctx, `
		UPDATE trip SET status = 'COMPLETED', completed_at = NOW()
		WHERE trip_id = $1
	`, tripID)
	if err != nil {
		log.Printf("stop-detector: complete trip %s: %v", tripID, err)
	} else {
		log.Printf("stop-detector: trip %s COMPLETED", tripID)
	}
}

func (s *StopDetector) loadActiveTrips(ctx context.Context) ([]string, error) {
	rows, err := s.db.Query(ctx, `SELECT trip_id FROM trip WHERE status = 'IN_PROGRESS'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	return ids, rows.Err()
}

func (s *StopDetector) loadTripStops(ctx context.Context, tripID string) ([]tripStopRow, error) {
	rows, err := s.db.Query(ctx, `
		SELECT t.trip_id, t.vehicle_id, vr.fleet_id,
		       rs.stop_id, rs.stop_name, rs.stop_sequence,
		       rs.lat, rs.lng, rs.arrival_radius_km,
		       COALESCE(tsp.status, 'PENDING'),
		       rs.stop_sequence = MAX(rs.stop_sequence) OVER (PARTITION BY rs.route_id)
		FROM trip t
		JOIN vehicle_registry vr     ON vr.vehicle_id = t.vehicle_id
		JOIN route_stops rs           ON rs.route_id   = t.route_id
		LEFT JOIN trip_stop_progress tsp
		       ON tsp.trip_id = t.trip_id AND tsp.stop_id = rs.stop_id
		WHERE t.trip_id = $1
		ORDER BY rs.stop_sequence
	`, tripID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []tripStopRow
	for rows.Next() {
		var r tripStopRow
		if err := rows.Scan(
			&r.tripID, &r.vehicleID, &r.fleetID,
			&r.stopID, &r.stopName, &r.stopSequence,
			&r.stopLat, &r.stopLng, &r.arrivalRadiusKm,
			&r.status, &r.isFinalStop,
		); err != nil {
			continue
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
