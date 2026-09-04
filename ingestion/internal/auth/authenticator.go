package auth

import (
	"context"
	"sync"
	"time"

	"fleet-monitor/ingestion/internal/config"
	"fleet-monitor/ingestion/internal/store"
)

type cacheEntry struct {
	fleetID   string
	expiresAt time.Time
}

type Authenticator struct {
	localCache sync.Map
	redis      apiKeyStore
	ttl        time.Duration
	staticKeys map[string]bool
}

// apiKeyStore keeps authentication testable without changing the production
// Redis implementation.
type apiKeyStore interface {
	GetAPIKey(context.Context, string) (string, error)
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
	_, ok := a.FleetID(ctx, apiKey)
	return ok
}

// FleetID authenticates apiKey and returns the fleet it is permitted to
// submit telemetry for. Static keys authenticate successfully, but have no
// fleet ownership and therefore return an empty fleet ID.
func (a *Authenticator) FleetID(ctx context.Context, apiKey string) (string, bool) {
	if apiKey == "" {
		return "", false
	}
	// Level 0: static config keys
	if a.staticKeys[apiKey] {
		return "", true
	}

	// Level 1: in-memory cache
	if raw, ok := a.localCache.Load(apiKey); ok {
		entry := raw.(cacheEntry)
		if time.Now().Before(entry.expiresAt) {
			return entry.fleetID, true
		}
		a.localCache.Delete(apiKey)
	}

	// Level 2: Redis lookup
	fleetID, err := a.redis.GetAPIKey(ctx, apiKey)
	if err != nil || fleetID == "" {
		return "", false
	}

	// Populate in-memory cache
	a.localCache.Store(apiKey, cacheEntry{
		fleetID:   fleetID,
		expiresAt: time.Now().Add(a.ttl),
	})

	return fleetID, true
}
