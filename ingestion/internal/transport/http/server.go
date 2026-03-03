package http

import (
	"context"
	"fmt"
	"net/http"

	"fleet-monitor/ingestion/internal/auth"
	"fleet-monitor/ingestion/internal/config"
	"fleet-monitor/ingestion/internal/metrics"
	"fleet-monitor/ingestion/internal/pipeline"
	"fleet-monitor/ingestion/internal/store"
)

type Server struct {
	httpServer *http.Server
}

func NewServer(
	cfg *config.Config,
	dispatcher *pipeline.Dispatcher,
	authenticator *auth.Authenticator,
	tsStore *store.TimescaleStore,
	redisStore *store.RedisStore,
) *Server {
	telemetryHandler := NewTelemetryHandler(dispatcher)
	healthHandler := NewHealthHandler(tsStore, redisStore)
	authMiddleware := NewAuthMiddleware(authenticator)

	mux := http.NewServeMux()

	mux.Handle(
		"POST /api/v1/telemetry",
		authMiddleware.Wrap(http.HandlerFunc(telemetryHandler.Handle)),
	)
	mux.HandleFunc("GET /health", healthHandler.Handle)
	mux.HandleFunc("GET /metrics", metrics.HandleMetrics)

	return &Server{
		httpServer: &http.Server{
			Addr:    ":" + cfg.HTTPPort,
			Handler: mux,
		},
	}
}

func (s *Server) Start() error {
	fmt.Printf("Listening on %s\n", s.httpServer.Addr)
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}
