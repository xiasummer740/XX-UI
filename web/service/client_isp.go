package service

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// IP 归属查询：走 ip-api.com 免费接口（45 次/分钟），结果缓存 24 小时。
// 客户端数量很少（几个到几十个），加上缓存后几乎不会有额外开销。
type ispCacheEntry struct {
	org string
	at  time.Time
}

var (
	ispCacheMu sync.Mutex
	ispCache   = map[string]ispCacheEntry{}
	ispClient  = &http.Client{Timeout: 3 * time.Second}
)

// lookupISP 返回某个 IP 的归属描述（"中国电信" 等），查不到返回空串。
func lookupISP(ip string) string {
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil {
		return ""
	}
	if parsed.IsPrivate() || parsed.IsLoopback() || parsed.IsLinkLocalUnicast() {
		return ""
	}
	ispCacheMu.Lock()
	if e, ok := ispCache[ip]; ok && time.Since(e.at) < 24*time.Hour {
		ispCacheMu.Unlock()
		return e.org
	}
	ispCacheMu.Unlock()

	url := fmt.Sprintf("http://ip-api.com/json/%s?fields=status,country,isp,as&lang=zh-CN", ip)
	resp, err := ispClient.Get(url)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var out struct {
		Status  string `json:"status"`
		Country string `json:"country"`
		ISP     string `json:"isp"`
		AS      string `json:"as"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.Status != "success" {
		return ""
	}
	org := strings.TrimSpace(out.ISP)
	if org == "" {
		org = strings.TrimSpace(out.AS)
	}
	if out.Country != "" && out.Country != "中国" && out.Country != "China" {
		org = out.Country + " " + org
	}
	ispCacheMu.Lock()
	ispCache[ip] = ispCacheEntry{org: org, at: time.Now()}
	ispCacheMu.Unlock()
	return org
}

// classifyISP 把归属描述归到三家运营商。
func classifyISP(org string) string {
	low := strings.ToLower(org)
	switch {
	case strings.Contains(org, "电信"), strings.Contains(low, "telecom"), strings.Contains(low, "chinanet"):
		return "电信"
	case strings.Contains(org, "联通"), strings.Contains(low, "unicom"), strings.Contains(low, "china169"):
		return "联通"
	case strings.Contains(org, "移动"), strings.Contains(low, "mobile"), strings.Contains(low, "cmcc"), strings.Contains(low, "cmnet"):
		return "移动"
	case strings.Contains(org, "教育"), strings.Contains(low, "cernet"), strings.Contains(low, "education"):
		return "教育网"
	case strings.Contains(org, "铁通"), strings.Contains(low, "tietong"):
		return "铁通"
	case strings.Contains(org, "广电"), strings.Contains(low, "broadnet"):
		return "广电"
	case org == "":
		return ""
	default:
		return "其他"
	}
}
