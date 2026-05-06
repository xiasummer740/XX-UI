// Package middleware provides HTTP middleware for the XX-UI web panel.
package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type visitor struct {
	count    int
	lastSeen time.Time
}

// RateLimiter returns a Gin middleware that limits each IP to `maxRequests` requests
// per `window` duration. Once the window expires, the counter resets.
func RateLimiter(maxRequests int, window time.Duration) gin.HandlerFunc {
	var mu sync.Mutex
	visitors := make(map[string]*visitor)

	// Background cleanup of stale entries
	go func() {
		for {
			time.Sleep(window)
			mu.Lock()
			for ip, v := range visitors {
				if time.Since(v.lastSeen) > window {
					delete(visitors, ip)
				}
			}
			mu.Unlock()
		}
	}()

	return func(c *gin.Context) {
		ip := c.ClientIP()
		mu.Lock()
		// Prune if map grows too large (potential attack)
		if len(visitors) > 50000 {
			now := time.Now()
			for k, v := range visitors {
				if now.Sub(v.lastSeen) > window {
					delete(visitors, k)
				}
			}
		}
		v, exists := visitors[ip]
		now := time.Now()
		if !exists {
			if len(visitors) > 50000 {
				mu.Unlock()
				c.Next()
				return
			}
			v = &visitor{count: 0, lastSeen: now}
			visitors[ip] = v
		}
		// Reset counter if window has passed
		if now.Sub(v.lastSeen) > window {
			v.count = 0
		}
		v.count++
		count := v.count
		v.lastSeen = now
		mu.Unlock()

		if count > maxRequests {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"success": false,
				"msg":     "Too many requests. Please try again later.",
			})
			return
		}
		c.Next()
	}
}
