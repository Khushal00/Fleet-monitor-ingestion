package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeValidator struct {
	fleetID string
	valid   bool
}

func (v fakeValidator) Validate(context.Context, string) bool {
	return v.valid
}

func (v fakeValidator) FleetID(context.Context, string) (string, bool) {
	return v.fleetID, v.valid
}

func TestFleetScopedRejectsUnknownFleetIdentity(t *testing.T) {
	tests := []struct {
		name           string
		keyFleet       string
		requestedFleet string
		wantStatus     int
	}{
		{
			name:           "unmapped key is forbidden for every fleet",
			keyFleet:       "",
			requestedFleet: "fleet_a",
			wantStatus:     http.StatusForbidden,
		},
		{
			name:           "matching fleet passes",
			keyFleet:       "fleet_a",
			requestedFleet: "fleet_a",
			wantStatus:     http.StatusOK,
		},
		{
			name:           "different fleet is forbidden",
			keyFleet:       "fleet_a",
			requestedFleet: "fleet_b",
			wantStatus:     http.StatusForbidden,
		},
		{
			name:       "missing requested fleet is bad request",
			keyFleet:   "fleet_a",
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/alerts", nil)
			req.Header.Set("X-API-Key", "test-key")
			if tt.requestedFleet != "" {
				q := req.URL.Query()
				q.Set("fleet_id", tt.requestedFleet)
				req.URL.RawQuery = q.Encode()
			}
			rr := httptest.NewRecorder()

			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
			handler := Auth(fakeValidator{fleetID: tt.keyFleet, valid: true})(FleetScoped("fleet_id")(next))
			handler.ServeHTTP(rr, req)

			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rr.Code, tt.wantStatus, rr.Body.String())
			}
		})
	}
}
