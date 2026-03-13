package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"fleet-monitor/serving/internal/auth"
	"fleet-monitor/serving/internal/config"
	"fleet-monitor/serving/internal/handler"
	"fleet-monitor/serving/internal/jobs"
	"fleet-monitor/serving/internal/middleware"
	"fleet-monitor/serving/internal/store"
	"fleet-monitor/serving/internal/ws"
)

func main() {
	_ = godotenv.Load()

	cfg := config.Load()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	tsStore, err := store.NewTimescaleStore(ctx, cfg)
	if err != nil {
		log.Fatalf("TimescaleDB: %v", err)
	}
	defer tsStore.Close()
	fmt.Println("✓ TimescaleDB connected")

	redisStore, err := store.NewRedisStore(ctx, cfg)
	if err != nil {
		log.Fatalf("Redis: %v", err)
	}
	defer redisStore.Close()
	fmt.Println("✓ Redis connected")

	authenticator := auth.NewAuthenticator(auth.Config{
		ValidAPIKeys:    cfg.ValidAPIKeys,
		CacheTTLSeconds: cfg.AuthCacheTTLSeconds,
	}, redisStore.Client())
	fmt.Println("✓ Authenticator ready")

	authMW := middleware.Auth(authenticator)

	hub := ws.NewHub(redisStore.Client(), authenticator)
	go hub.Run(ctx)
	fmt.Println("✓ WebSocket hub started")

	// ── Background jobs ───────────────────────────────────────────────────────

	go jobs.NewHeartbeatMonitor(
		redisStore.Client(), tsStore.Pool(), hub,
		cfg.HeartbeatIntervalSeconds, cfg.StalenessThresholdSeconds,
	).Run(ctx)
	fmt.Println("✓ Heartbeat monitor started")

	go jobs.NewDeviationDetector(
		redisStore.Client(), tsStore.Pool(), hub,
		cfg.DeviationDetectorIntervalSeconds,
	).Run(ctx)
	fmt.Println("✓ Deviation detector started")

	go jobs.NewStopDetector(
		redisStore.Client(), tsStore.Pool(), hub,
		cfg.StopDetectorIntervalSeconds,
	).Run(ctx)
	fmt.Println("✓ Stop detector started")

	go jobs.NewETAEstimator(
		redisStore.Client(), tsStore.Pool(),
		cfg.StopDetectorIntervalSeconds,
	).Run(ctx)
	fmt.Println("✓ ETA estimator started")

	// ── Handlers ─────────────────────────────────────────────────────────────

	healthHandler    := handler.NewHealthHandler(tsStore, redisStore)
	analyticsHandler := handler.NewAnalyticsHandler(redisStore.Client(), tsStore.Pool())
	vehicleHandler   := handler.NewVehicleHandler(redisStore.Client(), tsStore.Pool())
	alertHandler     := handler.NewAlertHandler(redisStore.Client(), tsStore.Pool())
	tripHandler      := handler.NewTripHandler(redisStore.Client(), tsStore.Pool())

	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", healthHandler.Handle)
	mux.HandleFunc("GET /ws", hub.ServeWS)

	mux.Handle("GET /api/v1/whoami",
		authMW(http.HandlerFunc(whoamiHandler)),
	)
	mux.Handle("GET /api/v1/fleet/{fleet_id}/ping",
		authMW(
			middleware.FleetScoped("fleet_id")(
				http.HandlerFunc(fleetPingHandler),
			),
		),
	)

	mux.Handle("GET /api/v1/analytics/summary",
		authMW(http.HandlerFunc(analyticsHandler.HandleSummary)))

	mux.Handle("GET /api/v1/vehicles/{vehicle_id}/panel",
		authMW(http.HandlerFunc(vehicleHandler.HandlePanel)))
	mux.Handle("GET /api/v1/vehicles/{vehicle_id}/active-trip",
		authMW(http.HandlerFunc(vehicleHandler.HandleActiveTrip)))
	mux.Handle("GET /api/v1/vehicles/{vehicle_id}/alerts",
		authMW(http.HandlerFunc(vehicleHandler.HandleVehicleAlerts)))

	mux.Handle("GET /api/v1/alerts",
		authMW(http.HandlerFunc(alertHandler.HandleAttentionQueue)))
	mux.Handle("GET /api/v1/alerts/{alert_id}",
		authMW(http.HandlerFunc(alertHandler.HandleAlertDetail)))
	mux.Handle("POST /api/v1/alerts/{alert_id}/acknowledge",
		authMW(http.HandlerFunc(alertHandler.HandleAcknowledge)))
	mux.Handle("POST /api/v1/alerts/{alert_id}/resolve",
		authMW(http.HandlerFunc(alertHandler.HandleResolve)))
	mux.Handle("POST /api/v1/alerts/{alert_id}/unacknowledge",
		authMW(http.HandlerFunc(alertHandler.HandleUnacknowledge)))
	mux.Handle("GET /api/v1/fleet/{fleet_id}/alerts",
		authMW(
			middleware.FleetScoped("fleet_id")(
				http.HandlerFunc(alertHandler.HandleFleetAlertHistory),
			),
		))

	mux.Handle("GET /api/v1/trips",
		authMW(http.HandlerFunc(tripHandler.HandleList)))
	mux.Handle("GET /api/v1/trips/{trip_id}",
		authMW(http.HandlerFunc(tripHandler.HandleDetail)))

	srv := &http.Server{
		Addr:    ":" + cfg.HTTPPort,
		Handler: mux,
	}

	go func() {
		fmt.Printf("✓ Serving layer listening on :%s\n", cfg.HTTPPort)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP server error: %v", err)
		}
	}()

	<-ctx.Done()
	fmt.Println("\nShutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("Shutdown error: %v", err)
	}

	fmt.Println("Done.")
}

func whoamiHandler(w http.ResponseWriter, r *http.Request) {
	apiKey  := middleware.APIKeyFromContext(r.Context())
	fleetID := middleware.FleetIDFromContext(r.Context())

	keySource := "redis"
	if fleetID == "" {
		keySource = "static_config"
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"api_key":    apiKey,
		"fleet_id":   fleetID,
		"key_source": keySource,
		"message":    "auth passed",
	})
}

func fleetPingHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"fleet_id": r.PathValue("fleet_id"),
		"message":  "fleet access authorised",
	})
}