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
	step2_online_status(ctx, client)
	step3_deviation_flags(ctx, client)
	step4_verify(ctx, client)

	fmt.Println("\n Redis seeded successfully")
	fmt.Println("   Run next: go test ./internal/store/... -v")
}

// ─────────────────────────────────────────────────────────────
// Step 1 — API keys
// ─────────────────────────────────────────────────────────────
func step1_api_keys(ctx context.Context, client *redis.Client) {
	fmt.Println("\n── Step 1: Seeding API keys ────────────────────")

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

// ─────────────────────────────────────────────────────────────
// Step 2 — Vehicle online status
// ─────────────────────────────────────────────────────────────
func step2_online_status(ctx context.Context, client *redis.Client) {
	fmt.Println("\n── Step 2: Seeding vehicle online status ────────")

	onlineStatus := map[string]string{
		"vehicle:VH_TEST_001:online_status": "online",
		"vehicle:VH_TEST_002:online_status": "online",
		"vehicle:VH_TEST_003:online_status": "offline",
	}

	for key, status := range onlineStatus {
		err := client.Set(ctx, key, status, 0).Err()
		if err != nil {
			log.Fatalf("Failed to set key %s: %v", key, err)
		}
		fmt.Printf("  ✓ %-45s → %s\n", key, status)
	}
}

// ─────────────────────────────────────────────────────────────
// Step 3 — Vehicle deviation flags
// ─────────────────────────────────────────────────────────────
func step3_deviation_flags(ctx context.Context, client *redis.Client) {
	fmt.Println("\n── Step 3: Seeding vehicle deviation flags ──────")

	deviationFlags := map[string]string{
		"vehicle:VH_TEST_001:deviation": "false",
		"vehicle:VH_TEST_002:deviation": "true",
		"vehicle:VH_TEST_003:deviation": "false",
	}

	for key, val := range deviationFlags {
		err := client.Set(ctx, key, val, 0).Err()
		if err != nil {
			log.Fatalf("Failed to set key %s: %v", key, err)
		}
		fmt.Printf("  ✓ %-45s → %s\n", key, val)
	}
}

// ─────────────────────────────────────────────────────────────
// Step 4 — Verification
// ─────────────────────────────────────────────────────────────
func step4_verify(ctx context.Context, client *redis.Client) {
	fmt.Println("\n── Step 4: Verification ────────────────────────")

	authKeys, err := client.Keys(ctx, "vehicle:auth:*").Result()
	if err != nil {
		log.Fatalf("Verification failed (auth keys): %v", err)
	}
	fmt.Printf("  ✓ %d API keys found in Redis\n", len(authKeys))

	statusKeys, err := client.Keys(ctx, "vehicle:*:online_status").Result()
	if err != nil {
		log.Fatalf("Verification failed (online status keys): %v", err)
	}
	fmt.Printf("  ✓ %d online status keys found in Redis\n", len(statusKeys))

	deviationKeys, err := client.Keys(ctx, "vehicle:*:deviation").Result()
	if err != nil {
		log.Fatalf("Verification failed (deviation keys): %v", err)
	}
	fmt.Printf("  ✓ %d deviation flag keys found in Redis\n", len(deviationKeys))

	spotChecks := []struct {
		key      string
		expected string
	}{
		{"vehicle:auth:test_key", "test_fleet"},
		{"vehicle:VH_TEST_001:online_status", "online"},
		{"vehicle:VH_TEST_003:online_status", "offline"},
		{"vehicle:VH_TEST_002:deviation", "true"},
		{"vehicle:VH_TEST_001:deviation", "false"},
	}

	for _, sc := range spotChecks {
		val, err := client.Get(ctx, sc.key).Result()
		if err != nil {
			log.Fatalf("Spot check failed for %s: %v", sc.key, err)
		}
		if val != sc.expected {
			log.Fatalf("Spot check mismatch for %s: got %q, want %q", sc.key, val, sc.expected)
		}
		fmt.Printf("  ✓ spot check: %-45s → %s\n", sc.key, val)
	}
}

func redisGetEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
