package auth

import (
	"testing"
	"time"
)

func TestRateLimiterPrunesExpiredKeys(t *testing.T) {
	limiter := NewRateLimiter(1, time.Millisecond)

	if !limiter.Allow("one") {
		t.Fatal("first key was not allowed")
	}
	if !limiter.Allow("two") {
		t.Fatal("second key was not allowed")
	}

	time.Sleep(2 * time.Millisecond)

	if !limiter.Allow("three") {
		t.Fatal("third key was not allowed after window elapsed")
	}
	if len(limiter.attempts) != 1 {
		t.Fatalf("attempt keys = %d, want 1 after pruning expired keys", len(limiter.attempts))
	}
}
