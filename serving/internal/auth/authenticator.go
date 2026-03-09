package auth

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

type cacheEntry struct {
	fleetID   string
	expiresAt time.Time
}

type Authenticator struct {
	localCache sync.Map
	redis      *redis.Client
	ttl        time.Duration
	staticKeys map[string]bool
}

type Config struct {
	ValidAPIKeys    []string
	CacheTTLSeconds int
}

func NewAuthenticator(cfg Config, redisClient *redis.Client) *Authenticator {
	ttl := time.Duration(cfg.CacheTTLSeconds) * time.Second
	if ttl == 0 {
		ttl = 300 * time.Second
	}

	staticKeys := make(map[string]bool, len(cfg.ValidAPIKeys))
	for _, k := range cfg.ValidAPIKeys {
		if k != "" {
			staticKeys[k] = true
		}
	}

	return &Authenticator{
		redis:      redisClient,
		ttl:        ttl,
		staticKeys: staticKeys,
	}
}

func (a *Authenticator) Validate(ctx context.Context, apiKey string) bool {
	if apiKey == "" {
		return false
	}

	// Level 0 — static config keys, zero I/O.
	if a.staticKeys[apiKey] {
		return true
	}

	// Level 1 — in-process cache.
	if raw, ok := a.localCache.Load(apiKey); ok {
		entry := raw.(cacheEntry)
		if time.Now().Before(entry.expiresAt) {
			return true
		}
		a.localCache.Delete(apiKey)
	}

	// Level 2 — Redis.
	fleetID, err := a.lookupRedis(ctx, apiKey)
	if err != nil || fleetID == "" {
		return false
	}

	a.localCache.Store(apiKey, cacheEntry{
		fleetID:   fleetID,
		expiresAt: time.Now().Add(a.ttl),
	})

	return true
}

func (a *Authenticator) FleetID(ctx context.Context, apiKey string) (string, bool) {
	if apiKey == "" {
		return "", false
	}

	if a.staticKeys[apiKey] {
		return "", true
	}

	if raw, ok := a.localCache.Load(apiKey); ok {
		entry := raw.(cacheEntry)
		if time.Now().Before(entry.expiresAt) {
			return entry.fleetID, true
		}
		a.localCache.Delete(apiKey)
	}

	fleetID, err := a.lookupRedis(ctx, apiKey)
	if err != nil || fleetID == "" {
		return "", false
	}

	a.localCache.Store(apiKey, cacheEntry{
		fleetID:   fleetID,
		expiresAt: time.Now().Add(a.ttl),
	})

	return fleetID, true
}

func (a *Authenticator) lookupRedis(ctx context.Context, apiKey string) (string, error) {
	key := fmt.Sprintf("vehicle:auth:%s", apiKey)
	val, err := a.redis.Get(ctx, key).Result()
	if err == redis.Nil {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("redis auth lookup failed: %w", err)
	}
	return val, nil
}
