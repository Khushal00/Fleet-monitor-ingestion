package auth

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

type fakeRedisGetter struct {
	mu     sync.Mutex
	values map[string]string
	calls  int
}

func (r *fakeRedisGetter) Get(_ context.Context, key string) *redis.StringCmd {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	value, ok := r.values[key]
	if !ok {
		return redis.NewStringResult("", redis.Nil)
	}
	return redis.NewStringResult(value, nil)
}

func TestFleetIDCachesOwnershipAndValidateUsesIt(t *testing.T) {
	r := &fakeRedisGetter{values: map[string]string{"vehicle:auth:key": "fleet-a"}}
	a := NewAuthenticator(Config{CacheTTLSeconds: 60}, nil)
	a.redis = r
	for range 2 {
		fleet, ok := a.FleetID(context.Background(), "key")
		if !ok || fleet != "fleet-a" {
			t.Fatalf("FleetID = %q, %v", fleet, ok)
		}
	}
	if !a.Validate(context.Background(), "key") {
		t.Fatal("cached key was rejected")
	}
	if r.calls != 1 {
		t.Fatalf("Redis calls = %d, want 1", r.calls)
	}
}

func TestFleetIDRejectsEmptyAndUnknownKeys(t *testing.T) {
	r := &fakeRedisGetter{values: map[string]string{}}
	a := NewAuthenticator(Config{CacheTTLSeconds: 1}, nil)
	a.redis = r
	if _, ok := a.FleetID(context.Background(), ""); ok {
		t.Fatal("empty key was accepted")
	}
	if _, ok := a.FleetID(context.Background(), "unknown"); ok {
		t.Fatal("unknown key was accepted")
	}
	if a.Validate(context.Background(), "") {
		t.Fatal("empty key validated")
	}
	_ = time.Second
}
