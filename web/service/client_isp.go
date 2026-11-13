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
type ipGeo struct {
	Country  string // 中国
	Province string // 广东
	City     string // 深圳
	Org      string // 中国电信 / Chinanet ...
}

type ispCacheEntry struct {
	geo ipGeo
	at  time.Time
}

var (
	ispCacheMu sync.Mutex
	ispCache   = map[string]ispCacheEntry{}
	ispClient  = &http.Client{Timeout: 3 * time.Second}
)

// lookupGeo 返回某个 IP 的省市与归属，查不到返回零值。
func lookupGeo(ip string) ipGeo {
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil {
		return ipGeo{}
	}
	if parsed.IsPrivate() || parsed.IsLoopback() || parsed.IsLinkLocalUnicast() {
		return ipGeo{}
	}
	ispCacheMu.Lock()
	if e, ok := ispCache[ip]; ok && time.Since(e.at) < 24*time.Hour {
		geo := e.geo
		ispCacheMu.Unlock()
		return geo
	}
	ispCacheMu.Unlock()

	url := fmt.Sprintf("http://ip-api.com/json/%s?fields=status,country,regionName,city,isp,as&lang=zh-CN", ip)
	resp, err := ispClient.Get(url)
	if err != nil {
		return ipGeo{}
	}
	defer resp.Body.Close()
	var out struct {
		Status     string `json:"status"`
		Country    string `json:"country"`
		RegionName string `json:"regionName"`
		City       string `json:"city"`
		ISP        string `json:"isp"`
		AS         string `json:"as"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.Status != "success" {
		return ipGeo{}
	}
	geo := ipGeo{Country: out.Country, Province: out.RegionName, City: out.City, Org: strings.TrimSpace(out.ISP)}
	if geo.Org == "" {
		geo.Org = strings.TrimSpace(out.AS)
	}
	ispCacheMu.Lock()
	ispCache[ip] = ispCacheEntry{geo: geo, at: time.Now()}
	ispCacheMu.Unlock()
	return geo
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

// shortRegion 把「广东省」「广西壮族自治区」这类名字压缩成「广东」「广西」。
func shortRegion(s string) string {
	s = strings.TrimSpace(s)
	for _, suffix := range []string{"壮族自治区", "回族自治区", "维吾尔自治区", "自治区", "特别行政区", "省", "市"} {
		if strings.HasSuffix(s, suffix) && len(s) > len(suffix) {
			s = strings.TrimSuffix(s, suffix)
			break
		}
	}
	return s
}
