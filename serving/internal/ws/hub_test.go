package ws

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeAuthenticator struct {
	fleet string
	ok    bool
}

func (a fakeAuthenticator) FleetID(_ context.Context, key string) (string, bool) {
	return a.fleet, a.ok && key != ""
}

func TestServeWSRejectsMissingAndCrossFleetTokensBeforeUpgrade(t *testing.T) {
	h := NewHub(nil, fakeAuthenticator{fleet: "fleet-a", ok: true})
	for _, tt := range []struct {
		target string
		want   int
	}{{"/ws", http.StatusBadRequest}, {"/ws?fleet_id=fleet-b&token=key", http.StatusUnauthorized}, {"/ws?fleet_id=fleet-a", http.StatusUnauthorized}} {
		w := httptest.NewRecorder()
		h.ServeWS(w, httptest.NewRequest(http.MethodGet, tt.target, nil))
		if w.Code != tt.want {
			t.Fatalf("%s status = %d, want %d", tt.target, w.Code, tt.want)
		}
	}
}
