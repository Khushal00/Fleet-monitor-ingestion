package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"fleet-monitor/ingestion/internal/auth"
	"fleet-monitor/ingestion/internal/config"
	"fleet-monitor/ingestion/internal/pipeline"
)

func TestTelemetryHandlerValidationAndBackpressure(t *testing.T) {
	d := pipeline.NewDispatcher(1, 1, 1)
	h := NewTelemetryHandler(d)
	tests := []struct {
		name, body string
		want       int
	}{
		{"invalid JSON", "{", http.StatusBadRequest},
		{"missing IDs", `{"timestamp":"2024-01-01T00:00:00Z"}`, http.StatusBadRequest},
		{"accepted", `{"vehicle_id":"v1","fleet_id":"f1","timestamp":"2024-01-01T00:00:00Z"}`, http.StatusAccepted},
		{"ingress full", `{"vehicle_id":"v2","fleet_id":"f1","timestamp":"2024-01-01T00:00:00Z"}`, http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/v1/telemetry", strings.NewReader(tt.body))
			r = r.WithContext(context.WithValue(r.Context(), contextKeyFleetID, "f1"))
			w := httptest.NewRecorder()
			h.Handle(w, r)
			if w.Code != tt.want {
				t.Fatalf("status = %d, want %d; body=%s", w.Code, tt.want, w.Body.String())
			}
			if got := w.Header().Get("Content-Type"); got != "application/json" {
				t.Fatalf("content type = %q", got)
			}
		})
	}
}

func TestTelemetryHandlerRequiresOwningFleet(t *testing.T) {
	d := pipeline.NewDispatcher(1, 1, 1)
	h := NewTelemetryHandler(d)
	body := `{"vehicle_id":"v1","fleet_id":"fleet-a","timestamp":"2024-01-01T00:00:00Z"}`

	for _, tt := range []struct {
		name, keyFleet string
		want           int
	}{
		{name: "matching fleet is accepted", keyFleet: "fleet-a", want: http.StatusAccepted},
		{name: "different fleet is forbidden", keyFleet: "fleet-b", want: http.StatusForbidden},
		{name: "missing identity is forbidden", keyFleet: "", want: http.StatusForbidden},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/v1/telemetry", strings.NewReader(body))
			if tt.name != "missing identity is forbidden" {
				r = r.WithContext(context.WithValue(r.Context(), contextKeyFleetID, tt.keyFleet))
			}
			w := httptest.NewRecorder()
			h.Handle(w, r)
			if w.Code != tt.want {
				t.Fatalf("status = %d, want %d; body=%s", w.Code, tt.want, w.Body.String())
			}
		})
	}
}

func TestTelemetryHandlerRejectsStaticKeyWithoutFleetOwnership(t *testing.T) {
	d := pipeline.NewDispatcher(1, 1, 1)
	h := NewTelemetryHandler(d)
	a := auth.NewAuthenticator(&config.Config{ValidAPIKeys: []string{"static-key"}}, nil)
	endpoint := NewAuthMiddleware(a).Wrap(http.HandlerFunc(h.Handle))

	r := httptest.NewRequest(http.MethodPost, "/api/v1/telemetry", strings.NewReader(
		`{"vehicle_id":"v1","fleet_id":"fleet-a","timestamp":"2024-01-01T00:00:00Z"}`,
	))
	r.Header.Set("X-API-Key", "static-key")
	w := httptest.NewRecorder()
	endpoint.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusForbidden, w.Body.String())
	}
}
