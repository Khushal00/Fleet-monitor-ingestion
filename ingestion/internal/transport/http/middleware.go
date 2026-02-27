package http

import (
	"net/http"

	"fleet-monitor/ingestion/internal/auth"
)

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

		if !m.auth.Validate(r.Context(), apiKey) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"invalid API key"}`))
			return
		}

		next.ServeHTTP(w, r)
	})
}
