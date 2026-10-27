// Package middleware provides HTTP middleware for the XX-UI web panel.
package middleware

import (
	"sync"
	"time"
)

// loginGuardMaxEntries bounds the tracking map so a flood of distinct addresses
// cannot grow it without limit.
const loginGuardMaxEntries = 20000

type loginAttempt struct {
	failures int
	lastSeen time.Time
	lockedAt time.Time
}

// LoginGuard tracks failed authentication attempts and temporarily locks a key
// after repeated failures. Callers choose the key, so the same guard can enforce
// a strict per-user policy and a looser per-address policy (which also covers
// username spraying).
//
// Entries are purely in-memory: a restart clears every lock, which is acceptable
// because the guard only slows down online brute-force attempts.
type LoginGuard struct {
	mu       sync.Mutex
	attempts map[string]*loginAttempt
	max      int
	window   time.Duration
}

// NewLoginGuard creates a guard that locks a key after max failures inside window.
func NewLoginGuard(max int, window time.Duration) *LoginGuard {
	if max <= 0 {
		max = 5
	}
	if window <= 0 {
		window = 15 * time.Minute
	}
	guard := &LoginGuard{
		attempts: make(map[string]*loginAttempt),
		max:      max,
		window:   window,
	}
	go guard.cleanupLoop()
	return guard
}

// Locked reports whether the key is locked right now, and for how much longer.
func (g *LoginGuard) Locked(key string) (bool, time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()

	attempt, ok := g.attempts[key]
	if !ok {
		return false, 0
	}
	if attempt.lockedAt.IsZero() {
		// Tracked but still below the limit; forget it once the window expires.
		if time.Since(attempt.lastSeen) > g.window {
			delete(g.attempts, key)
		}
		return false, 0
	}
	remaining := g.window - time.Since(attempt.lockedAt)
	if remaining <= 0 {
		delete(g.attempts, key)
		return false, 0
	}
	return true, remaining
}

// Fail records one failed attempt and locks the key once the limit is reached.
func (g *LoginGuard) Fail(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	now := time.Now()
	if len(g.attempts) >= loginGuardMaxEntries {
		g.prune(now)
	}

	attempt, ok := g.attempts[key]
	if !ok {
		if len(g.attempts) >= loginGuardMaxEntries {
			// Still full after pruning: drop the oldest entry we can find
			// instead of growing without bound.
			g.evictOldest()
		}
		attempt = &loginAttempt{}
		g.attempts[key] = attempt
	}

	if time.Since(attempt.lastSeen) > g.window {
		attempt.failures = 0
		attempt.lockedAt = time.Time{}
	}
	attempt.failures++
	attempt.lastSeen = now
	if attempt.failures >= g.max {
		attempt.lockedAt = now
	}
}

// Reset clears the tracking state for a key, e.g. after a successful login.
func (g *LoginGuard) Reset(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.attempts, key)
}

func (g *LoginGuard) cleanupLoop() {
	ticker := time.NewTicker(g.window)
	defer ticker.Stop()
	for range ticker.C {
		g.mu.Lock()
		g.prune(time.Now())
		g.mu.Unlock()
	}
}

// prune drops entries that are neither locked nor seen inside the window.
func (g *LoginGuard) prune(now time.Time) {
	for key, attempt := range g.attempts {
		locked := !attempt.lockedAt.IsZero() && now.Sub(attempt.lockedAt) < g.window
		if !locked && now.Sub(attempt.lastSeen) > g.window {
			delete(g.attempts, key)
		}
	}
}

// evictOldest removes the least recently seen entry to keep the map bounded.
func (g *LoginGuard) evictOldest() {
	var oldestKey string
	var oldestTime time.Time
	first := true
	for key, attempt := range g.attempts {
		if first || attempt.lastSeen.Before(oldestTime) {
			oldestKey = key
			oldestTime = attempt.lastSeen
			first = false
		}
	}
	if !first {
		delete(g.attempts, oldestKey)
	}
}
