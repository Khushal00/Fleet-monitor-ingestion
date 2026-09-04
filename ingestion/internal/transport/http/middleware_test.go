package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"fleet-monitor/ingestion/internal/auth"
	"fleet-monitor/ingestion/internal/config"
)

func TestAuthMiddlewareRejectsMissingAndInvalidKeys(t *testing.T) {
	a := auth.NewAuthenticator(&config.Config{ValidAPIKeys: []string{"valid"}}, nil)
	h := NewAuthMiddleware(a).Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for _, tt := range []struct {
		key  string
		want int
	}{{"", http.StatusUnauthorized}, {"valid", http.StatusNoContent}} {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		if tt.key != "" {
			r.Header.Set("X-API-Key", tt.key)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tt.want {
			t.Fatalf("key %q status = %d, want %d", tt.key, w.Code, tt.want)
		}
	}
}
