package admin

import (
	"sync"
	"time"
)

type RateLimiter interface {
	Allow(key string, now time.Time) bool
}

type MemoryRateLimiter struct {
	mu       sync.Mutex
	limit    int
	window   time.Duration
	counters map[string]rateCounter
}

type rateCounter struct {
	count     int
	windowEnd time.Time
}

func NewMemoryRateLimiter(limit int, window time.Duration) *MemoryRateLimiter {
	return &MemoryRateLimiter{
		limit:    limit,
		window:   window,
		counters: map[string]rateCounter{},
	}
}

func (l *MemoryRateLimiter) Allow(key string, now time.Time) bool {
	if l == nil || l.limit <= 0 || l.window <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	counter := l.counters[key]
	if counter.windowEnd.IsZero() || !counter.windowEnd.After(now) {
		counter = rateCounter{windowEnd: now.Add(l.window)}
	}
	if counter.count >= l.limit {
		l.counters[key] = counter
		return false
	}
	counter.count++
	l.counters[key] = counter
	return true
}
