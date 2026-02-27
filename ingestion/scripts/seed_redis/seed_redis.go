package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file — using system environment variables")
	}

	client := redis.NewClient(&redis.Options{
		Addr:     redisGetEnv("REDIS_ADDR", "localhost:6379"),
		Password: redisGetEnv("REDIS_PASSWORD", ""),
		DB:       0,
	})
	defer client.Close()

	ctx := context.Background()

	fmt.Println("Connecting to Redis...")
	if err := client.Ping(ctx).Err(); err != nil {
		log.Fatalf("Connection failed: %v\n\nMake sure Redis is running:\n  docker-compose up -d redis", err)
	}
	fmt.Println("✓ Connected")

	step1_api_keys(ctx, client)
	step2_verify(ctx, client)

	fmt.Println("\n✅ Redis seeded successfully")
	fmt.Println("   Run next: go test ./internal/store/... -v")
}

func step1_api_keys(ctx context.Context, client *redis.Client) {
	fmt.Println("\n── Step 1: Seeding API keys ────────────────────")

	// Key pattern: vehicle:auth:{api_key} → fleet_id
	// This is what authenticator.go looks up at Level 2
	// TTL = 0 means permanent — these never expire
	apiKeys := map[string]string{
		"vehicle:auth:fleet_delhi_jaipur_key": "fleet_delhi_jaipur",
		"vehicle:auth:fleet_mumbai_pune_key":  "fleet_mumbai_pune",
		"vehicle:auth:fleet_bangalore_key":    "fleet_bangalore",
		"vehicle:auth:test_key":               "test_fleet",
	}

	for key, fleetID := range apiKeys {
		err := client.Set(ctx, key, fleetID, 0).Err()
		if err != nil {
			log.Fatalf("Failed to set key %s: %v", key, err)
		}
		fmt.Printf("  ✓ %-45s → %s\n", key, fleetID)
	}
}

func step2_verify(ctx context.Context, client *redis.Client) {
	fmt.Println("\n── Step 2: Verification ────────────────────────")

	// Check all keys exist
	keys, err := client.Keys(ctx, "vehicle:auth:*").Result()
	if err != nil {
		log.Fatalf("Verification failed: %v", err)
	}
	fmt.Printf("  ✓ %d API keys found in Redis\n", len(keys))

	// Spot check one key
	val, err := client.Get(ctx, "vehicle:auth:test_key").Result()
	if err != nil {
		log.Fatalf("Spot check failed: %v", err)
	}
	fmt.Printf("  ✓ spot check: vehicle:auth:test_key → %s\n", val)
}

func redisGetEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
