// Package middleware provides HTTP middleware for the XX-UI web panel.
package middleware

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/XiaSummer740/XX-UI/logger"
	"github.com/gin-gonic/gin"
)

type visitor struct {
	count       int
	windowStart time.Time
	lastSeen    time.Time
}

// hit 记录一次访问，返回「当前窗口内」的访问次数。
// 窗口从第一次访问开始计时，到点清零 —— 与请求频率无关。
func (v *visitor) hit(now time.Time, window time.Duration) int {
	if v.windowStart.IsZero() || now.Sub(v.windowStart) >= window {
		v.count = 0
		v.windowStart = now
	}
	v.count++
	v.lastSeen = now
	return v.count
}

// RateLimiter returns a Gin middleware that limits each IP to `maxRequests` requests
// per `window` duration.
//
// 采用固定窗口：窗口从该 IP 的第一次请求开始计时，到点清零。
// （旧实现用 lastSeen 判断是否过期，而 lastSeen 每次请求都会刷新，
// 结果是只要浏览器不断有请求，计数器永远不清零 —— 面板开着几个小时
// 就会莫名其妙地永久 429，刷新也没用。）
func RateLimiter(maxRequests int, window time.Duration) gin.HandlerFunc {
	var mu sync.Mutex
	visitors := make(map[string]*visitor)

	// Background cleanup of stale entries
	go func() {
		for {
			time.Sleep(window)
			mu.Lock()
			for ip, v := range visitors {
				if time.Since(v.lastSeen) > 3*window {
					delete(visitors, ip)
				}
			}
			mu.Unlock()
		}
	}()

	return func(c *gin.Context) {
		ip := c.ClientIP()
		now := time.Now()

		mu.Lock()
		// Prune if map grows too large (potential attack)
		if len(visitors) > 50000 {
			for k, v := range visitors {
				if now.Sub(v.lastSeen) > window {
					delete(visitors, k)
				}
			}
		}
		v, exists := visitors[ip]
		if !exists {
			v = &visitor{}
			visitors[ip] = v
		}
		count := v.hit(now, window)
		retryAfter := int(window.Seconds()-now.Sub(v.windowStart).Seconds()) + 1
		mu.Unlock()

		if count > maxRequests {
			if retryAfter < 1 {
				retryAfter = 1
			}
			logger.Warningf("[limit] %s 触发限流：窗口内第 %d 次请求 %s", ip, count, c.Request.URL.Path)
			c.Header("Retry-After", strconv.Itoa(retryAfter))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"success": false,
				"message": "请求太频繁了，请等几秒再刷新（面板没有坏）",
				"msg":     "请求太频繁了，请等几秒再刷新（面板没有坏）",
			})
			return
		}
		c.Next()
	}
}
