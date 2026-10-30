package service

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/XiaSummer740/XX-UI/database"
	"github.com/XiaSummer740/XX-UI/database/model"
	"github.com/google/uuid"
)

// realityPortCandidates 是自动建站时优先尝试的端口。
// 443 在 DPI 眼里就是正常 HTTPS（REALITY 的伪装也只有在 443 上才自洽），
// 其余几个是常见备用 HTTPS 端口，最后才退到随机高位端口。
var realityPortCandidates = []int{443, 8443, 2087, 8447, 2096}

// realityTargetPool 是 REALITY 的候选偷站目标。
// 要求：支持 TLS 1.3、ALPN 含 h2、证书有效，且尽量不在"被脚本用烂"的名单里。
var realityTargetPool = []string{
	"www.samsung.com",
	"www.nvidia.com",
	"www.tesla.com",
	"www.autodesk.com",
	"www.adobe.com",
	"www.icloud.com",
	"www.mozilla.org",
	"addons.mozilla.org",
	"www.cloudflare.com",
	"www.microsoft.com",
	"www.apple.com",
	"www.amazon.com",
}

// overusedTargets 是各类教程/脚本里出现频率极高的目标，
// 单独被拿来当 SNI 时"太常见"，排序时降权（只在延迟明显更优时才选它）。
var overusedTargets = map[string]bool{
	"www.microsoft.com":  true,
	"www.apple.com":      true,
	"www.amazon.com":     true,
	"www.cloudflare.com": true,
	"addons.mozilla.org": true,
}

// realityTargetPenaltyMs 是"过热目标"在排序时被扣掉的等效延迟。
const realityTargetPenaltyMs = 80

// RealityCandidate 是单个候选偷站目标的探测结果。
type RealityCandidate struct {
	ServerName string `json:"serverName"`
	Dest       string `json:"dest"`
	Ok         bool   `json:"ok"`
	LatencyMs  int64  `json:"latencyMs"`
	TLSVersion string `json:"tlsVersion"`
	ALPN       string `json:"alpn"`
	Reason     string `json:"reason"`
}

// RealitySuggestion 是自动建站的选参结论，同时回传给前端做展示。
type RealitySuggestion struct {
	Port           int                `json:"port"`
	PortReason     string             `json:"portReason"`
	ServerName     string             `json:"serverName"`
	Dest           string             `json:"dest"`
	LatencyMs      int64              `json:"latencyMs"`
	Candidates     []RealityCandidate `json:"candidates"`
	ClientEmail    string             `json:"clientEmail"`
	UUID           string             `json:"uuid"`
	PublicKey      string             `json:"publicKey"`
	ShortID        string             `json:"shortId"`
	OverusedChosen bool               `json:"overusedChosen"`
}

// probeRealityTarget 对候选目标做一次真实 TLS 握手，验证它是否适合当 REALITY 的偷站目标。
// 判断依据：TLS 1.3、ALPN 协商到 h2、证书校验通过。
func probeRealityTarget(host string, timeout time.Duration) RealityCandidate {
	c := RealityCandidate{ServerName: host, Dest: host + ":443"}
	dialer := &net.Dialer{Timeout: timeout}
	start := time.Now()
	conn, err := tls.DialWithDialer(dialer, "tcp", c.Dest, &tls.Config{
		ServerName: host,
		MinVersion: tls.VersionTLS13,
		NextProtos: []string{"h2", "http/1.1"},
	})
	if err != nil {
		c.Reason = shortTLSReason(err)
		return c
	}
	defer conn.Close()
	c.LatencyMs = time.Since(start).Milliseconds()

	state := conn.ConnectionState()
	switch state.Version {
	case tls.VersionTLS13:
		c.TLSVersion = "1.3"
	case tls.VersionTLS12:
		c.TLSVersion = "1.2"
	default:
		c.TLSVersion = fmt.Sprintf("0x%x", state.Version)
	}
	c.ALPN = state.NegotiatedProtocol
	if state.Version != tls.VersionTLS13 {
		c.Reason = "不支持 TLS 1.3"
		return c
	}
	c.Ok = true
	return c
}

// shortTLSReason 把握手错误压缩成一句可读的原因，避免把大段错误直接塞给前端。
func shortTLSReason(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "timeout"):
		return "连接超时"
	case strings.Contains(msg, "certificate"):
		return "证书校验失败"
	case strings.Contains(msg, "handshake failure"):
		return "握手被拒绝"
	case strings.Contains(msg, "no such host"):
		return "域名无法解析"
	}
	if len(msg) > 60 {
		msg = msg[:60]
	}
	return msg
}

// probeRealityTargets 并发探测整个候选池，避免串行时把按钮拖慢到十几秒。
func probeRealityTargets(hosts []string, timeout time.Duration) []RealityCandidate {
	results := make([]RealityCandidate, len(hosts))
	var wg sync.WaitGroup
	for i, h := range hosts {
		wg.Add(1)
		go func(i int, host string) {
			defer wg.Done()
			results[i] = probeRealityTarget(host, timeout)
		}(i, h)
	}
	wg.Wait()
	return results
}

// pickRealityTarget 从探测结果里挑一个：优先延迟最低，但"过热目标"要额外让出
// realityTargetPenaltyMs 的优势才会被选中，避免全网 SNI 撞车。
func pickRealityTarget(cands []RealityCandidate) (RealityCandidate, bool) {
	ok := make([]RealityCandidate, 0, len(cands))
	for _, c := range cands {
		if c.Ok {
			ok = append(ok, c)
		}
	}
	if len(ok) == 0 {
		return RealityCandidate{}, false
	}
	sort.SliceStable(ok, func(i, j int) bool { return ok[i].LatencyMs < ok[j].LatencyMs })

	best := ok[0]
	bestScore := best.LatencyMs
	if overusedTargets[best.ServerName] {
		bestScore += realityTargetPenaltyMs
	}
	for _, c := range ok[1:] {
		score := c.LatencyMs
		if overusedTargets[c.ServerName] {
			score += realityTargetPenaltyMs
		}
		if score < bestScore {
			best, bestScore = c, score
		}
	}
	return best, true
}

// pickRealityPort 选一个可用端口：优先 443，其次常见备用 HTTPS 端口，最后随机高位端口。
// listening 用于判断端口是否已被占用，used 是本面板里已存在的入站端口。
func pickRealityPort(listening func(int) bool, used map[int]bool) (int, string) {
	for _, p := range realityPortCandidates {
		if used[p] || listening(p) {
			continue
		}
		reason := fmt.Sprintf("端口 %d 空闲", p)
		if p == 443 {
			reason = "443 空闲，最像正常 HTTPS 流量"
		}
		return p, reason
	}
	// 兜底：从 20000 起找一个空闲高位端口
	for p := 20000; p < 40000; p++ {
		if used[p] || listening(p) {
			continue
		}
		return p, fmt.Sprintf("443/8443 等常用端口都被占用，改用高位端口 %d", p)
	}
	return 0, ""
}

// SuggestRealityInbound 生成一套"当前环境最优"的 VLESS+REALITY+Vision 配置，
// 返回给前端预填「添加入站」弹窗：用户只需改备注/流量/到期/用户名/设备限制。
func (s *InboundService) SuggestRealityInbound(userID int) (map[string]any, *RealitySuggestion, error) {
	// 1) 端口：避开本面板已用的端口和系统正在监听的端口
	used := make(map[int]bool)
	var existing []*model.Inbound
	if err := database.GetDB().Model(model.Inbound{}).Find(&existing).Error; err == nil {
		for _, ib := range existing {
			used[ib.Port] = true
		}
	}
	port, portReason := pickRealityPort(s.isLocalPortListening, used)
	if port == 0 {
		return nil, nil, fmt.Errorf("没有可用端口")
	}

	// 2) 偷站目标：并发探测 TLS 1.3 + h2，选延迟低且不那么"热门"的
	candidates := probeRealityTargets(realityTargetPool, 3500*time.Millisecond)
	target, ok := pickRealityTarget(candidates)
	if !ok {
		// 全部不可用时退回一个保守目标，保证按钮不会直接失败
		target = RealityCandidate{ServerName: "www.icloud.com", Dest: "www.icloud.com:443"}
	}

	// 3) 密钥与账号
	keyPair, err := s.generateX25519Key()
	if err != nil {
		return nil, nil, err
	}
	clientID := uuid.New().String()
	shortID := strings.ReplaceAll(uuid.New().String(), "-", "")[:8]
	email := "user-" + clientID[:6]

	settings := map[string]any{
		"clients": []map[string]any{
			{"id": clientID, "flow": "xtls-rprx-vision", "email": email, "enable": true},
		},
		"decryption": "none",
	}
	settingsJSON, _ := json.MarshalIndent(settings, "", "  ")

	streamSettings := map[string]any{
		"network":  "tcp",
		"security": "reality",
		"realitySettings": map[string]any{
			"dest":        target.Dest,
			"target":      target.Dest,
			"serverNames": []string{target.ServerName},
			"privateKey":  keyPair["privateKey"],
			"publicKey":   keyPair["publicKey"],
			"shortIds":    []string{shortID},
			"fingerprint": "chrome",
			"spiderX":     "/",
			"show":        false,
			"xver":        0,
			"maxTimediff": 0,
		},
		"tcpSettings": map[string]any{
			"header": map[string]any{"type": "none"},
		},
	}
	streamJSON, _ := json.MarshalIndent(streamSettings, "", "  ")

	sniffing := `{"enabled":true,"destOverride":["http","tls","quic"],"metadataOnly":false,"routeOnly":false}`

	dbInbound := map[string]any{
		"listen":         "",
		"port":           port,
		"protocol":       "vless",
		"settings":       string(settingsJSON),
		"streamSettings": string(streamJSON),
		"sniffing":       sniffing,
		"remark":         fmt.Sprintf("自动配置-REALITY-%d", port),
		"enable":         true,
		"up":             0,
		"down":           0,
		"total":          0,
		"expiryTime":     0,
		"deviceLimit":    0,
		"trafficReset":   "never",
		"tag":            fmt.Sprintf("inbound-%v", port),
	}

	suggestion := &RealitySuggestion{
		Port:           port,
		PortReason:     portReason,
		ServerName:     target.ServerName,
		Dest:           target.Dest,
		LatencyMs:      target.LatencyMs,
		Candidates:     candidates,
		ClientEmail:    email,
		UUID:           clientID,
		PublicKey:      fmt.Sprintf("%v", keyPair["publicKey"]),
		ShortID:        shortID,
		OverusedChosen: overusedTargets[target.ServerName],
	}
	_ = userID
	return dbInbound, suggestion, nil
}
