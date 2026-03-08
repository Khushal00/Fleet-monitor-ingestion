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
	healthHandler := handler.NewHealthHandler(tsStore, redisStore)

	// --- Routes ---
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", healthHandler.Handle)

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