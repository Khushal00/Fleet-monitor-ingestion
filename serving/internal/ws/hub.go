package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

type fleetSubscription struct {
	clients      map[*Client]struct{}
	telemetrySub *redis.PubSub
	alertsSub    *redis.PubSub
	cancel       context.CancelFunc
}

type broadcastMsg struct {
	fleetID string
	data    []byte
}

type Authenticator interface {
	Validate(ctx context.Context, apiKey string) bool
}

type Hub struct {
	redis         *redis.Client
	authenticator Authenticator
	register      chan *Client
	unregister    chan *Client
	broadcast     chan broadcastMsg
	mu            sync.RWMutex
	fleets        map[string]*fleetSubscription
}

func NewHub(redisClient *redis.Client, auth Authenticator) *Hub {
	return &Hub{
		redis:         redisClient,
		authenticator: auth,
		register:      make(chan *Client, 64),
		unregister:    make(chan *Client, 64),
		broadcast:     make(chan broadcastMsg, 1024),
		fleets:        make(map[string]*fleetSubscription),
	}
}

func (h *Hub) Run(ctx context.Context) {
	for {
		select {
		case client := <-h.register:
			h.addClient(ctx, client)
		case client := <-h.unregister:
			h.removeClient(client)
		case msg := <-h.broadcast:
			h.fanOut(msg)
		case <-ctx.Done():
			h.shutdown()
			return
		}
	}
}

func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	fleetID := r.URL.Query().Get("fleet_id")
	token := r.URL.Query().Get("token")

	if fleetID == "" {
		http.Error(w, `{"error":"fleet_id query parameter required"}`, http.StatusBadRequest)
		return
	}
	if !h.authenticator.Validate(r.Context(), token) {
		http.Error(w, `{"error":"invalid or missing token"}`, http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws: upgrade failed for fleet %s: %v", fleetID, err)
		return
	}

	client := newClient(h, conn, fleetID)
	h.register <- client
	go client.writePump()
	go client.readPump()
}

func (h *Hub) addClient(ctx context.Context, client *Client) {
	fleet, exists := h.fleets[client.fleetID]
	if !exists {
		fleet = h.startFleetSubscriptions(ctx, client.fleetID)
		h.fleets[client.fleetID] = fleet
	}
	fleet.clients[client] = struct{}{}
	log.Printf("ws: client connected to fleet %s (total: %d)", client.fleetID, len(fleet.clients))
}

func (h *Hub) removeClient(client *Client) {
	fleet, exists := h.fleets[client.fleetID]
	if !exists {
		return
	}
	if _, ok := fleet.clients[client]; ok {
		delete(fleet.clients, client)
		close(client.send)
		log.Printf("ws: client disconnected from fleet %s (remaining: %d)", client.fleetID, len(fleet.clients))
	}
	if len(fleet.clients) == 0 {
		fleet.cancel()
		fleet.telemetrySub.Close()
		fleet.alertsSub.Close()
		delete(h.fleets, client.fleetID)
		log.Printf("ws: fleet %s has no clients, subscriptions cancelled", client.fleetID)
	}
}

func (h *Hub) fanOut(msg broadcastMsg) {
	fleet, exists := h.fleets[msg.fleetID]
	if !exists {
		return
	}
	for client := range fleet.clients {
		if !client.enqueue(msg.data) {
			log.Printf("ws: evicting slow client from fleet %s", msg.fleetID)
			delete(fleet.clients, client)
			close(client.send)
		}
	}
}

func (h *Hub) shutdown() {
	for fleetID, fleet := range h.fleets {
		fleet.cancel()
		fleet.telemetrySub.Close()
		fleet.alertsSub.Close()
		for client := range fleet.clients {
			close(client.send)
		}
		delete(h.fleets, fleetID)
	}
	log.Println("ws: hub shutdown complete")
}

func (h *Hub) startFleetSubscriptions(ctx context.Context, fleetID string) *fleetSubscription {
	subCtx, cancel := context.WithCancel(ctx)

	telemetrySub := h.redis.Subscribe(subCtx, fmt.Sprintf("fleet:%s:telemetry", fleetID))
	alertsSub := h.redis.Subscribe(subCtx, fmt.Sprintf("fleet:%s:alerts", fleetID))

	fleet := &fleetSubscription{
		clients:      make(map[*Client]struct{}),
		telemetrySub: telemetrySub,
		alertsSub:    alertsSub,
		cancel:       cancel,
	}

	go h.drainTelemetry(subCtx, fleetID, telemetrySub)
	go h.drainAlerts(subCtx, fleetID, alertsSub)

	log.Printf("ws: started subscriptions for fleet %s", fleetID)
	return fleet
}

func (h *Hub) drainTelemetry(ctx context.Context, fleetID string, sub *redis.PubSub) {
	ch := sub.Channel()
	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				return
			}
			var raw struct {
				VehicleID  string  `json:"vehicle_id"`
				Lat        float64 `json:"lat"`
				Lng        float64 `json:"lng"`
				SpeedKmh   float64 `json:"speed_kmh"`
				IsMoving   bool    `json:"is_moving"`
				EngineOn   bool    `json:"engine_on"`
				ReceivedAt int64   `json:"received_at"`
			}
			if err := json.Unmarshal([]byte(msg.Payload), &raw); err != nil {
				log.Printf("ws: telemetry decode error for fleet %s: %v", fleetID, err)
				continue
			}
			evt := newPositionEvent(VehiclePositionPayload{
				VehicleID:  raw.VehicleID,
				Lat:        raw.Lat,
				Lng:        raw.Lng,
				SpeedKmh:   raw.SpeedKmh,
				IsMoving:   raw.IsMoving,
				EngineOn:   raw.EngineOn,
				ReceivedAt: time.Unix(raw.ReceivedAt, 0).UTC(),
			})
			data, err := json.Marshal(evt)
			if err != nil {
				log.Printf("ws: telemetry encode error for fleet %s: %v", fleetID, err)
				continue
			}
			select {
			case h.broadcast <- broadcastMsg{fleetID: fleetID, data: data}:
			case <-ctx.Done():
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

func (h *Hub) drainAlerts(ctx context.Context, fleetID string, sub *redis.PubSub) {
	ch := sub.Channel()
	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				return
			}
			var raw struct {
				VehicleID   string  `json:"vehicle_id"`
				AlertType   string  `json:"alert_type"`
				Severity    string  `json:"severity"`
				Value       float64 `json:"value"`
				TriggeredAt int64   `json:"triggered_at"`
			}
			if err := json.Unmarshal([]byte(msg.Payload), &raw); err != nil {
				log.Printf("ws: alert decode error for fleet %s: %v", fleetID, err)
				continue
			}
			evt := newAlertEvent(VehicleAlertPayload{
				AlertID:        0,
				VehicleID:      raw.VehicleID,
				AlertType:      raw.AlertType,
				Severity:       raw.Severity,
				TriggeredValue: raw.Value,
				CreatedAt:      time.Unix(raw.TriggeredAt, 0).UTC(),
			})
			data, err := json.Marshal(evt)
			if err != nil {
				log.Printf("ws: alert encode error for fleet %s: %v", fleetID, err)
				continue
			}
			select {
			case h.broadcast <- broadcastMsg{fleetID: fleetID, data: data}:
			case <-ctx.Done():
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

func (h *Hub) BroadcastOffline(fleetID string, payload VehicleOfflinePayload) {
	h.broadcastEvent(fleetID, newOfflineEvent(payload))
}

func (h *Hub) BroadcastOnline(fleetID string, payload VehicleOnlinePayload) {
	h.broadcastEvent(fleetID, newOnlineEvent(payload))
}

func (h *Hub) BroadcastDeviation(fleetID string, payload VehicleDeviationPayload) {
	h.broadcastEvent(fleetID, newDeviationEvent(payload))
}

func (h *Hub) BroadcastStopArrived(fleetID string, payload StopArrivedPayload) {
	h.broadcastEvent(fleetID, newStopArrivedEvent(payload))
}

func (h *Hub) BroadcastAlertResolved(fleetID string, payload AlertResolvedPayload) {
	h.broadcastEvent(fleetID, newAlertResolvedEvent(payload))
}

func (h *Hub) broadcastEvent(fleetID string, evt envelope) {
	data, err := json.Marshal(evt)
	if err != nil {
		log.Printf("ws: broadcast marshal error for fleet %s: %v", fleetID, err)
		return
	}
	select {
	case h.broadcast <- broadcastMsg{fleetID: fleetID, data: data}:
	default:
		log.Printf("ws: broadcast channel full, dropping event for fleet %s", fleetID)
	}
}

func (h *Hub) ClientCount(fleetID string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if fleet, ok := h.fleets[fleetID]; ok {
		return len(fleet.clients)
	}
	return 0
}
