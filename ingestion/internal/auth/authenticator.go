package auth

import (
	"context"
	"sync"
	"time"

	"fleet-monitor/ingestion/internal/config"
	"fleet-monitor/ingestion/internal/store"
)

type cacheEntry struct {
	vehicleID string
	expiresAt time.Time
}

type Authenticator struct {
	localCache sync.Map
	redis      *store.RedisStore
	ttl        time.Duration
	staticKeys map[string]bool
}

func NewAuthenticator(cfg *config.Config, redis *store.RedisStore) *Authenticator {
	staticKeys := make(map[string]bool, len(cfg.ValidAPIKeys))
	for _, k := range cfg.ValidAPIKeys {
		if k != "" {
			staticKeys[k] = true
		}
	}

	return &Authenticator{
		redis:      redis,
		ttl:        time.Duration(cfg.AuthCacheTTLSeconds) * time.Second,
		staticKeys: staticKeys,
	}
}

func (a *Authenticator) Validate(ctx context.Context, apiKey string) bool {
	// Level 0: static config keys
	if a.staticKeys[apiKey] {
		return true
	}

	// Level 1: in-memory cache
	if raw, ok := a.localCache.Load(apiKey); ok {
		entry := raw.(cacheEntry)
		if time.Now().Before(entry.expiresAt) {
			return true
		}
		a.localCache.Delete(apiKey)
	}

	// Level 2: Redis lookup
	vehicleID, err := a.redis.GetAPIKey(ctx, apiKey)
	if err != nil || vehicleID == "" {
		return false
	}

	// Populate in-memory cache
	a.localCache.Store(apiKey, cacheEntry{
		vehicleID: vehicleID,
		expiresAt: time.Now().Add(a.ttl),
	})

	return true
}
