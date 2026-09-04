package auth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"fleet-monitor/ingestion/internal/config"
)

type fakeAPIKeyStore struct {
	mu    sync.Mutex
	keys  map[string]string
	calls int
	err   error
}

func (s *fakeAPIKeyStore) GetAPIKey(_ context.Context, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.keys[key], s.err
}

func TestValidateCachesRedisResultAndRejectsEmptyKey(t *testing.T) {
	store := &fakeAPIKeyStore{keys: map[string]string{"redis-key": "fleet-a"}}
	a := NewAuthenticator(&config.Config{AuthCacheTTLSeconds: 60}, nil)
	a.redis = store

	if a.Validate(context.Background(), "") {
		t.Fatal("empty API key was accepted")
	}
	if !a.Validate(context.Background(), "redis-key") || !a.Validate(context.Background(), "redis-key") {
		t.Fatal("valid Redis key was rejected")
	}
	if store.calls != 1 {
		t.Fatalf("Redis calls = %d, want 1 after cache hit", store.calls)
	}
}

func TestFleetIDCachesOwnershipUntilExpiry(t *testing.T) {
	store := &fakeAPIKeyStore{keys: map[string]string{"redis-key": "fleet-a"}}
	a := NewAuthenticator(&config.Config{AuthCacheTTLSeconds: 60}, nil)
	a.redis = store

	if fleetID, ok := a.FleetID(context.Background(), "redis-key"); !ok || fleetID != "fleet-a" {
		t.Fatalf("FleetID() = %q, %t; want fleet-a, true", fleetID, ok)
	}
	store.keys["redis-key"] = "fleet-b"
	if fleetID, ok := a.FleetID(context.Background(), "redis-key"); !ok || fleetID != "fleet-a" {
		t.Fatalf("cached FleetID() = %q, %t; want fleet-a, true", fleetID, ok)
	}
	if store.calls != 1 {
		t.Fatalf("Redis calls = %d, want 1 after cache hit", store.calls)
	}
}

func TestFleetIDRefreshesRemapAndRevocationAfterExpiry(t *testing.T) {
	store := &fakeAPIKeyStore{keys: map[string]string{"redis-key": "fleet-a"}}
	a := NewAuthenticator(&config.Config{AuthCacheTTLSeconds: 0}, nil)
	a.redis = store

	if fleetID, ok := a.FleetID(context.Background(), "redis-key"); !ok || fleetID != "fleet-a" {
		t.Fatalf("FleetID() = %q, %t; want fleet-a, true", fleetID, ok)
	}
	store.keys["redis-key"] = "fleet-b"
	if fleetID, ok := a.FleetID(context.Background(), "redis-key"); !ok || fleetID != "fleet-b" {
		t.Fatalf("remapped FleetID() = %q, %t; want fleet-b, true", fleetID, ok)
	}
	delete(store.keys, "redis-key")
	if fleetID, ok := a.FleetID(context.Background(), "redis-key"); ok || fleetID != "" {
		t.Fatalf("revoked FleetID() = %q, %t; want empty, false", fleetID, ok)
	}
}

func TestValidateExpiresCacheAndRejectsLookupFailure(t *testing.T) {
	store := &fakeAPIKeyStore{keys: map[string]string{"key": "vehicle-a"}}
	a := NewAuthenticator(&config.Config{AuthCacheTTLSeconds: 0}, nil)
	a.redis = store
	if !a.Validate(context.Background(), "key") || !a.Validate(context.Background(), "key") {
		t.Fatal("valid key was rejected")
	}
	if store.calls != 2 {
		t.Fatalf("Redis calls = %d, want 2 for expired zero TTL cache", store.calls)
	}
	store.err = errors.New("redis unavailable")
	if a.Validate(context.Background(), "missing") {
		t.Fatal("lookup failure was accepted")
	}
}

func TestStaticKeyHasNoFleetOwnershipAndAvoidsRedis(t *testing.T) {
	store := &fakeAPIKeyStore{err: errors.New("must not be called")}
	a := NewAuthenticator(&config.Config{ValidAPIKeys: []string{"static"}, AuthCacheTTLSeconds: 1}, nil)
	a.redis = store
	if fleetID, ok := a.FleetID(context.Background(), "static"); !ok || fleetID != "" {
		t.Fatalf("FleetID(static) = %q, %t; want empty, true", fleetID, ok)
	}
	if store.calls != 0 {
		t.Fatalf("Redis calls = %d, want 0", store.calls)
	}
	// Keep time imported so this test explicitly documents that static keys do
	// not depend on cache expiry.
	_ = time.Second
}
