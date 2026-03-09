package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"fleet-monitor/serving/internal/config"
	"fleet-monitor/serving/internal/handler"
	"fleet-monitor/serving/internal/store"
)

func main() {
	_ = godotenv.Load()

	cfg := config.Load()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// --- Stores ---
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

	// --- Handlers ---
	healthHandler    := handler.NewHealthHandler(tsStore, redisStore)
	analyticsHandler := handler.NewAnalyticsHandler(redisStore.Client(), tsStore.Pool())
	vehicleHandler   := handler.NewVehicleHandler(redisStore.Client(), tsStore.Pool())
	alertHandler     := handler.NewAlertHandler(redisStore.Client(), tsStore.Pool())
	tripHandler      := handler.NewTripHandler(redisStore.Client(), tsStore.Pool())

	// --- Routes ---
	mux := http.NewServeMux()

	// Health
	mux.HandleFunc("GET /health", healthHandler.Handle)

	// Analytics
	mux.HandleFunc("GET /api/v1/analytics/summary", analyticsHandler.HandleSummary)

	// Vehicle
	mux.HandleFunc("GET /api/v1/vehicles/{vehicle_id}/panel",        vehicleHandler.HandlePanel)
	mux.HandleFunc("GET /api/v1/vehicles/{vehicle_id}/active-trip",  vehicleHandler.HandleActiveTrip)
	mux.HandleFunc("GET /api/v1/vehicles/{vehicle_id}/alerts",       vehicleHandler.HandleVehicleAlerts)

	// Alerts
	mux.HandleFunc("GET  /api/v1/alerts",                                alertHandler.HandleAttentionQueue)
	mux.HandleFunc("GET  /api/v1/alerts/{alert_id}",                     alertHandler.HandleAlertDetail)
	mux.HandleFunc("POST /api/v1/alerts/{alert_id}/acknowledge",         alertHandler.HandleAcknowledge)
	mux.HandleFunc("POST /api/v1/alerts/{alert_id}/resolve",             alertHandler.HandleResolve)
	mux.HandleFunc("POST /api/v1/alerts/{alert_id}/unacknowledge",       alertHandler.HandleUnacknowledge)
	mux.HandleFunc("GET  /api/v1/fleet/{fleet_id}/alerts",               alertHandler.HandleFleetAlertHistory)

	// Trips
	mux.HandleFunc("GET /api/v1/trips",             tripHandler.HandleList)
	mux.HandleFunc("GET /api/v1/trips/{trip_id}",   tripHandler.HandleDetail)

	// --- HTTP Server ---
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

	// --- Wait for shutdown signal ---
	<-ctx.Done()
	fmt.Println("\nShutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("Shutdown error: %v", err)
	}

	fmt.Println("Done.")
}