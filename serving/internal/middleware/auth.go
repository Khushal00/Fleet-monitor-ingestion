package middleware

import (
	"context"
	"encoding/json"
	"net/http"
)

type contextKey int

const (
	contextKeyAPIKey contextKey = iota

	contextKeyFleetID
)

type Validator interface {
	Validate(ctx context.Context, apiKey string) bool
	FleetID(ctx context.Context, apiKey string) (string, bool)
}

type errorResponse struct {
	Error string `json:"error"`
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(errorResponse{Error: msg})
}

func Auth(v Validator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiKey := r.Header.Get("X-API-Key")
			if apiKey == "" {
				writeError(w, http.StatusUnauthorized, "missing X-API-Key header")
				return
			}

			fleetID, ok := v.FleetID(r.Context(), apiKey)
			if !ok {
				writeError(w, http.StatusUnauthorized, "invalid API key")
				return
			}

			ctx := context.WithValue(r.Context(), contextKeyAPIKey, apiKey)
			ctx = context.WithValue(ctx, contextKeyFleetID, fleetID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func FleetScoped(fleetIDParam string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestedFleet := r.URL.Query().Get(fleetIDParam)
			if requestedFleet == "" {
				// Path value fallback (Go 1.22+).
				requestedFleet = r.PathValue(fleetIDParam)
			}

			if requestedFleet == "" {
				writeError(w, http.StatusBadRequest, fleetIDParam+" is required")
				return
			}

			keyFleet := FleetIDFromContext(r.Context())

			if keyFleet != "" && keyFleet != requestedFleet {
				writeError(w, http.StatusForbidden,
					"API key is not authorised for fleet "+requestedFleet)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func APIKeyFromContext(ctx context.Context) string {
	v, _ := ctx.Value(contextKeyAPIKey).(string)
	return v
}

func FleetIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(contextKeyFleetID).(string)
	return v
}
