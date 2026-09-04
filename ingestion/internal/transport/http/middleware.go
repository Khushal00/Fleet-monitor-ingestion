package http

import (
	"context"
	"net/http"

	"fleet-monitor/ingestion/internal/auth"
)

type contextKey int

const contextKeyFleetID contextKey = iota

type AuthMiddleware struct {
	auth *auth.Authenticator
}

func NewAuthMiddleware(a *auth.Authenticator) *AuthMiddleware {
	return &AuthMiddleware{auth: a}
}

func (m *AuthMiddleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey := r.Header.Get("X-API-Key")
		if apiKey == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"missing X-API-Key header"}`))
			return
		}

		fleetID, ok := m.auth.FleetID(r.Context(), apiKey)
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"invalid API key"}`))
			return
		}

		ctx := context.WithValue(r.Context(), contextKeyFleetID, fleetID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func fleetIDFromContext(ctx context.Context) string {
	fleetID, _ := ctx.Value(contextKeyFleetID).(string)
	return fleetID
}
