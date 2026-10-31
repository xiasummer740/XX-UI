package sub

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// 订阅防泄漏：订阅链接一旦被转发出去，别人也能直接拉走节点。
// 这里做三件事（默认关闭，开启后生效）：
//   1) UA 白名单：只允许常见客户端的 User-Agent（严格模式）
//   2) 频率限制：同一 IP 每分钟最多 N 次订阅请求
//   3) 异常拉黑：短时间内反复违规的 IP 直接封一段时间

// SubGuard 保存限流/拉黑的内存状态。
type SubGuard struct {
	mu       sync.Mutex
	hits     map[string][]int64 // IP -> 最近一分钟内的请求时间戳
	blocked  map[string]int64   // IP -> 解封时间戳
	violate  map[string]int     // IP -> 违规次数（滑动窗口内）
	lastSeen map[string]int64   // IP -> 最近一次违规时间
}

// NewSubGuard 创建防泄漏中间件。
func NewSubGuard() *SubGuard {
	return &SubGuard{
		hits:     make(map[string][]int64),
		blocked:  make(map[string]int64),
		violate:  make(map[string]int),
		lastSeen: make(map[string]int64),
	}
}

// allowedUAMarkers 常见订阅客户端关键字（小写匹配）。
var allowedUAMarkers = []string{
	"v2ray", "v2rayn", "v2rayng", "shadowrocket", "clash", "clashx", "mihomo",
	"sing-box", "singbox", "sfa", "sfm", "sfi", "stash", "surge", "quantumult",
	"loon", "streisand", "hiddify", "nekobox", "nekoray", "kitsunebi", "passwall",
	"openclash", "shellclash", "verge", "flclash", "sub-store", "substore", "curl",
	"mozilla", "safari", "okhttp", "go-http-client",
}

// isAllowedUA 判断 UA 是否在白名单内。
func isAllowedUA(ua string) bool {
	if ua == "" {
		return false
	}
	lower := strings.ToLower(ua)
	for _, marker := range allowedUAMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// Guard 是 gin 中间件：按配置做 UA 校验、限流与拉黑。
func (g *SubGuard) Guard(enable bool, strictUA bool, rpm int, blockMinutes int) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !enable {
			c.Next()
			return
		}
		if rpm <= 0 {
			rpm = 30
		}
		if blockMinutes <= 0 {
			blockMinutes = 30
		}
		ip := c.ClientIP()
		now := time.Now()
		nowUnix := now.Unix()

		g.mu.Lock()

		// 1) 已在黑名单
		if until, ok := g.blocked[ip]; ok && until > nowUnix {
			g.mu.Unlock()
			c.AbortWithStatus(http.StatusTooManyRequests)
			return
		}
		delete(g.blocked, ip)

		// 2) UA 校验（严格模式）
		if strictUA && !isAllowedUA(c.GetHeader("User-Agent")) {
			g.violate[ip]++
			g.lastSeen[ip] = nowUnix
			blocked := g.violate[ip] >= 5
			if blocked {
				g.blocked[ip] = nowUnix + int64(blockMinutes)*60
				g.violate[ip] = 0
			}
			g.mu.Unlock()
			if blocked {
				c.AbortWithStatus(http.StatusTooManyRequests)
				return
			}
			c.AbortWithStatus(http.StatusForbidden)
			return
		}

		// 3) 每分钟频率限制（滑动窗口）
		window := now.Add(-time.Minute).Unix()
		kept := g.hits[ip][:0]
		for _, ts := range g.hits[ip] {
			if ts > window {
				kept = append(kept, ts)
			}
		}
		kept = append(kept, nowUnix)
		g.hits[ip] = kept
		if len(kept) > rpm {
			g.violate[ip]++
			g.lastSeen[ip] = nowUnix
			if g.violate[ip] >= 5 {
				g.blocked[ip] = nowUnix + int64(blockMinutes)*60
				g.violate[ip] = 0
			}
			g.mu.Unlock()
			c.AbortWithStatus(http.StatusTooManyRequests)
			return
		}

		// 4) 顺带清理太久没动过的记录，避免内存无限增长
		if len(g.lastSeen) > 2048 {
			cut := nowUnix - 3600
			for k, v := range g.lastSeen {
				if v < cut {
					delete(g.lastSeen, k)
					delete(g.violate, k)
					delete(g.hits, k)
					delete(g.blocked, k)
				}
			}
		}
		g.mu.Unlock()

		c.Next()
	}
}
