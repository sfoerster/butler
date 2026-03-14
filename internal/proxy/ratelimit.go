package proxy

import (
	"sync"
	"time"

	"github.com/sfoerster/butler/internal/config"
)

type window struct {
	start time.Time
	count int
}

type rateLimiter struct {
	mu      sync.Mutex
	windows map[string]*window
	now     func() time.Time
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{
		windows: make(map[string]*window),
		now:     time.Now,
	}
}

// Allow checks whether a request from key is permitted under the given spec.
// A nil spec always allows the request.
func (rl *rateLimiter) Allow(key string, spec *config.RateSpec) bool {
	if spec == nil {
		return true
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.now()
	w, ok := rl.windows[key]
	if !ok || now.Sub(w.start) >= spec.Window {
		rl.windows[key] = &window{start: now, count: 1}
		return true
	}

	if w.count >= spec.Count {
		return false
	}
	w.count++
	return true
}
