package http

import (
	"encoding/json"
	"fleet-monitor/ingestion/internal/store"
	"net/http"
	"time"
)

type HealthHandler struct {
	tsStore    *store.TimescaleStore
	redisStore *store.RedisStore
}

func NewHealthHandler(ts *store.TimescaleStore, redis *store.RedisStore) *HealthHandler {
	return &HealthHandler{
		tsStore:    ts,
		redisStore: redis,
	}
}

type healthResponse struct {
	Status       string            `json:"status"`
	Service      string            `json:"service"`
	Timestamp    time.Time         `json:"timestamp"`
	Dependencies map[string]string `json:"dependencies"`
}

func (h *HealthHandler) Handle(w http.ResponseWriter, r *http.Request) {
	deps := make(map[string]string)
	healthy := true

	if err := h.tsStore.Ping(r.Context()); err != nil {
		deps["timescaledb"] = "unhealthy" + err.Error()
		healthy = false
	} else {
		deps["timescaledb"] = "healthy"
	}

	if err := h.redisStore.Ping(r.Context()); err != nil {
		deps["redis"] = "unhealthy" + err.Error()
		healthy = false
	} else {
		deps["redis"] = "healthy"
	}

	status := "healthy"
	httpStatus := http.StatusOK
	if !healthy {
		status = "degraded"
		httpStatus = http.StatusServiceUnavailable
	}
	resp := healthResponse{
		Status:       status,
		Service:      "ingestion",
		Timestamp:    time.Now().UTC(),
		Dependencies: deps,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	json.NewEncoder(w).Encode(resp)
}
