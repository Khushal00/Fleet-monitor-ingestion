package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"fleet-monitor/serving/internal/store"
)

type HealthHandler struct {
	tsStore *store.TimescaleStore
	rdStore *store.RedisStore
}

func NewHealthHandler(ts *store.TimescaleStore, rd *store.RedisStore) *HealthHandler {
	return &HealthHandler{
		tsStore: ts,
		rdStore: rd,
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
		deps["timescaledb"] = "unhealthy:" + err.Error()
		healthy = false
	} else {
		deps["timescaledb"] = "healthy"
	}

	if err := h.rdStore.Ping(r.Context()); err != nil {
		deps["redis"] = "unhealthy:" + err.Error()
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
		Service:      "serving",
		Timestamp:    time.Now().UTC(),
		Dependencies: deps,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	json.NewEncoder(w).Encode(resp)
}
