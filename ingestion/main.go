package main

import (
	"context"
	"fmt"
	"log"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"fleet-monitor/ingestion/internal/auth"
	"fleet-monitor/ingestion/internal/config"
	"fleet-monitor/ingestion/internal/pipeline"
	"fleet-monitor/ingestion/internal/store"
	transport "fleet-monitor/ingestion/internal/transport/http"
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

	authenticator := auth.NewAuthenticator(cfg, redisStore)
	fmt.Println("✓ Authenticator ready")

	dispatcher := pipeline.NewDispatcher(
		cfg.DBChannelSize,
		cfg.StateChannelSize,
		cfg.AlertChannelSize,
	)
	fmt.Println("✓ Dispatcher created")

	for i := 0; i < cfg.DBWriterWorkers; i++ {
		w := pipeline.NewDBWriter(dispatcher.DBChan, tsStore, cfg.DBBatchSize, cfg.DBFlushIntervalMS)
		go w.Run(ctx)
	}
	fmt.Printf("✓ %d DB writers started\n", cfg.DBWriterWorkers)

	for i := 0; i < cfg.StateWriterWorkers; i++ {
		w := pipeline.NewStateWriter(dispatcher.StateChan, redisStore)
		go w.Run(ctx)
	}
	fmt.Printf("✓ %d state writers started\n", cfg.StateWriterWorkers)

	for i := 0; i < cfg.AlertWorkers; i++ {
		e := pipeline.NewAlertEvaluator(dispatcher.AlertChan, tsStore, redisStore)
		go e.Run(ctx)
	}
	fmt.Printf("✓ %d alert evaluators started\n", cfg.AlertWorkers)

	server := transport.NewServer(cfg, dispatcher, authenticator, tsStore, redisStore)
	go func() {
		if err := server.Start(); err != nil {
			log.Printf("HTTP server: %v", err)
		}
	}()
	fmt.Println("✓ Service ready")

	<-ctx.Done()
	fmt.Println("\nShutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	server.Shutdown(shutdownCtx)

	time.Sleep(2 * time.Second)
	fmt.Println("Done.")
}
