package handlers

import (
	"testing"
	"time"
)

func TestTTLCacheExpiresAndRefreshes(t *testing.T) {
	cache := newTTLCache[string](50 * time.Millisecond)
	cache.Set("user", "alice")

	got, ok := cache.Get("user")
	if !ok || got != "alice" {
		t.Fatalf("cache deveria retornar o valor inicial, got=%q ok=%v", got, ok)
	}

	time.Sleep(80 * time.Millisecond)
	if _, ok := cache.Get("user"); ok {
		t.Fatal("cache deveria expirar após o TTL")
	}

	cache.Set("user", "bob")
	got, ok = cache.Get("user")
	if !ok || got != "bob" {
		t.Fatalf("cache deveria aceitar novo valor após expirar, got=%q ok=%v", got, ok)
	}
}
