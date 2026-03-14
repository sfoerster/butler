package proxy

import (
	"sync"
	"testing"
	"time"

	"github.com/sfoerster/butler/internal/config"
)

func TestRateLimiterAllowUpToCount(t *testing.T) {
	rl := newRateLimiter()
	spec := &config.RateSpec{Count: 3, Window: time.Minute}

	for i := range 3 {
		if !rl.Allow("client-a", spec) {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}
	if rl.Allow("client-a", spec) {
		t.Error("request 4 should be denied")
	}
}

func TestRateLimiterWindowReset(t *testing.T) {
	now := time.Now()
	rl := newRateLimiter()
	rl.now = func() time.Time { return now }
	spec := &config.RateSpec{Count: 2, Window: time.Minute}

	rl.Allow("k", spec)
	rl.Allow("k", spec)
	if rl.Allow("k", spec) {
		t.Fatal("should be denied before window reset")
	}

	// Advance past window
	now = now.Add(time.Minute + time.Second)
	if !rl.Allow("k", spec) {
		t.Error("should be allowed after window reset")
	}
}

func TestRateLimiterIndependentKeys(t *testing.T) {
	rl := newRateLimiter()
	spec := &config.RateSpec{Count: 1, Window: time.Minute}

	if !rl.Allow("a", spec) {
		t.Error("key a first request should be allowed")
	}
	if !rl.Allow("b", spec) {
		t.Error("key b first request should be allowed")
	}
	if rl.Allow("a", spec) {
		t.Error("key a second request should be denied")
	}
}

func TestRateLimiterNilSpecAlwaysAllows(t *testing.T) {
	rl := newRateLimiter()
	for range 100 {
		if !rl.Allow("any", nil) {
			t.Fatal("nil spec should always allow")
		}
	}
}

func TestRateLimiterConcurrent(t *testing.T) {
	rl := newRateLimiter()
	spec := &config.RateSpec{Count: 50, Window: time.Minute}

	var wg sync.WaitGroup
	allowed := make(chan bool, 200)

	for range 200 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			allowed <- rl.Allow("concurrent", spec)
		}()
	}
	wg.Wait()
	close(allowed)

	var trueCount int
	for a := range allowed {
		if a {
			trueCount++
		}
	}
	if trueCount != 50 {
		t.Errorf("allowed %d requests, want exactly 50", trueCount)
	}
}

func TestRateLimiterHourWindow(t *testing.T) {
	now := time.Now()
	rl := newRateLimiter()
	rl.now = func() time.Time { return now }
	spec := &config.RateSpec{Count: 1, Window: time.Hour}

	if !rl.Allow("k", spec) {
		t.Fatal("first request should be allowed")
	}
	if rl.Allow("k", spec) {
		t.Fatal("second request should be denied within hour")
	}

	// Advance 59 minutes — still denied
	now = now.Add(59 * time.Minute)
	if rl.Allow("k", spec) {
		t.Fatal("should still be denied before hour passes")
	}

	// Advance past hour
	now = now.Add(2 * time.Minute)
	if !rl.Allow("k", spec) {
		t.Error("should be allowed after hour reset")
	}
}
