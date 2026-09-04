package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlersRejectMissingRequiredRouteValuesBeforeDataAccess(t *testing.T) {
	alert := NewAlertHandler(nil, nil)
	vehicle := NewVehicleHandler(nil, nil)
	trip := NewTripHandler(nil, nil)
	analytics := NewAnalyticsHandler(nil, nil)
	cases := []struct {
		name string
		h    http.HandlerFunc
		path string
		body string
		want int
	}{
		{"attention fleet", alert.HandleAttentionQueue, "/alerts", "", http.StatusBadRequest},
		{"alert detail id", alert.HandleAlertDetail, "/alerts", "", http.StatusBadRequest},
		{"ack id", alert.HandleAcknowledge, "/alerts", `{}`, http.StatusBadRequest},
		{"resolve body", alert.HandleResolve, "/alerts/1", `{}`, http.StatusBadRequest},
		{"unacknowledge id", alert.HandleUnacknowledge, "/alerts", "", http.StatusBadRequest},
		{"fleet history", alert.HandleFleetAlertHistory, "/alerts", "", http.StatusBadRequest},
		{"vehicle panel", vehicle.HandlePanel, "/vehicles", "", http.StatusBadRequest},
		{"vehicle trip", vehicle.HandleActiveTrip, "/vehicles", "", http.StatusBadRequest},
		{"vehicle alerts", vehicle.HandleVehicleAlerts, "/vehicles", "", http.StatusBadRequest},
		{"trip detail", trip.HandleDetail, "/trips", "", http.StatusBadRequest},
		{"analytics fleet", analytics.HandleSummary, "/analytics", "", http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			tc.h(w, r)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", w.Code, tc.want, w.Body.String())
			}
			if got := w.Header().Get("Content-Type"); got != "application/json" {
				t.Fatalf("content type = %q", got)
			}
		})
	}
}
