package service

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/XiaSummer740/XX-UI/database"
	"github.com/XiaSummer740/XX-UI/database/model"
)

// 节点真实可用性探测。
//
// 「延迟」如果只是浏览器到面板的 RTT，所有节点都一样，看不出节点好坏。
// 这里在服务端真起一个临时 xray 客户端，用该入站自己的配置连上去，
// 真拉一次 generate_204：拿到 204 才算「这个节点给谁都能用」。

const (
	nodeProbeStateKey = "nodeProbeState"
	// 同一节点两次实测的最小间隔
	nodeProbeInterval = 10 * time.Minute
)

// NodeProbeResult 是单个节点的一次实测结果。
type NodeProbeResult struct {
	OK         bool   `json:"ok"`
	DelayMs    int64  `json:"delayMs"`
	StatusCode int    `json:"statusCode"`
	Error      string `json:"error,omitempty"`
	Address    string `json:"address,omitempty"`
	At         int64  `json:"at"`
}

func (s *InboundService) loadNodeProbeState() map[int]NodeProbeResult {
	state := map[int]NodeProbeResult{}
	var ss SettingService
	raw, err := ss.getString(nodeProbeStateKey)
	if err != nil || raw == "" {
		return state
	}
	_ = json.Unmarshal([]byte(raw), &state)
	if state == nil {
		state = map[int]NodeProbeResult{}
	}
	return state
}

func (s *InboundService) saveNodeProbeState(state map[int]NodeProbeResult) {
	bs, err := json.Marshal(state)
	if err != nil {
		return
	}
	var ss SettingService
	_ = ss.setString(nodeProbeStateKey, string(bs))
}

// NodeProbeSnapshot 供面板读取。
func (s *InboundService) NodeProbeSnapshot() map[int]NodeProbeResult {
	return s.loadNodeProbeState()
}

// probeAddress 选一个尽量贴近真实用户的连接地址：
// 有公网 IPv4 就用公网 IP（顺便验证防火墙/监听），否则退回 127.0.0.1。
func probeAddress(port int) string {
	addrs, err := net.InterfaceAddrs()
	if err == nil {
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
				if v4 := ipnet.IP.To4(); v4 != nil && !v4.IsPrivate() {
					return fmt.Sprintf("%s:%d", v4.String(), port)
				}
			}
		}
	}
	return fmt.Sprintf("127.0.0.1:%d", port)
}

// buildProbeOutbound 把一个入站转成指向它自己的客户端出站配置。
func buildProbeOutbound(ib *model.Inbound, address string, port int) (string, error) {
	host := address
	stream := map[string]any{}
	if strings.TrimSpace(ib.StreamSettings) != "" {
		if err := json.Unmarshal([]byte(ib.StreamSettings), &stream); err != nil {
			return "", fmt.Errorf("解析 streamSettings 失败: %v", err)
		}
	}
	// 客户端侧不需要 privateKey，需要由它推导出的 publicKey
	if rs, ok := stream["realitySettings"].(map[string]any); ok {
		privateKey, _ := rs["privateKey"].(string)
		if privateKey == "" {
			if settings, ok2 := rs["settings"].(map[string]any); ok2 {
				privateKey, _ = settings["privateKey"].(string)
			}
		}
		pub, err := derivePublicKey(privateKey)
		if err != nil {
			return "", err
		}
		clientRS := map[string]any{"publicKey": pub, "fingerprint": "chrome"}
		if names, ok2 := rs["serverNames"].([]any); ok2 && len(names) > 0 {
			clientRS["serverName"] = names[0]
		}
		if ids, ok2 := rs["shortIds"].([]any); ok2 && len(ids) > 0 {
			clientRS["shortId"] = ids[0]
		}
		stream = map[string]any{"network": stream["network"], "security": "reality", "realitySettings": clientRS}
		if stream["network"] == nil {
			stream["network"] = "tcp"
		}
	}

	var clients []map[string]any
	settings := map[string]any{}
	if strings.TrimSpace(ib.Settings) != "" {
		if err := json.Unmarshal([]byte(ib.Settings), &settings); err != nil {
			return "", fmt.Errorf("解析 settings 失败: %v", err)
		}
	}
	if arr, ok := settings["clients"].([]any); ok {
		for _, it := range arr {
			if m, ok2 := it.(map[string]any); ok2 {
				if enabled, has := m["enable"].(bool); has && !enabled {
					continue
				}
				clients = append(clients, m)
			}
		}
	}
	if len(clients) == 0 {
		return "", fmt.Errorf("没有可用客户端")
	}
	c := clients[0]

	outbound := map[string]any{
		"tag":            fmt.Sprintf("probe-%d", ib.Id),
		"protocol":       string(ib.Protocol),
		"streamSettings": stream,
	}

	switch string(ib.Protocol) {
	case "vless":
		user := map[string]any{"id": c["id"], "encryption": "none"}
		if flow, ok := c["flow"].(string); ok && flow != "" {
			user["flow"] = flow
		}
		outbound["settings"] = map[string]any{
			"vnext": []any{map[string]any{
				"address": host, "port": port, "users": []any{user},
			}},
		}
	case "vmess":
		outbound["settings"] = map[string]any{
			"vnext": []any{map[string]any{
				"address": host, "port": port,
				"users": []any{map[string]any{"id": c["id"], "security": "auto"}},
			}},
		}
	case "trojan":
		outbound["settings"] = map[string]any{
			"servers": []any{map[string]any{
				"address": host, "port": port, "password": c["password"],
			}},
		}
	case "shadowsocks":
		outbound["settings"] = map[string]any{
			"servers": []any{map[string]any{
				"address": host, "port": port,
				"method": settings["method"], "password": settings["password"],
			}},
		}
	default:
		return "", fmt.Errorf("暂不支持 %s 协议的真实探测", ib.Protocol)
	}

	bs, err := json.Marshal(outbound)
	if err != nil {
		return "", err
	}
	return string(bs), nil
}

// probeInboundUsability 对单个入站做一次真实代理测试。
func (s *InboundService) probeInboundUsability(ib *model.Inbound) NodeProbeResult {
	res := NodeProbeResult{At: time.Now().Unix()}
	addr := probeAddress(ib.Port)
	res.Address = addr
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	outboundJSON, err := buildProbeOutbound(ib, host, port)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	var os OutboundService
	r, err := os.TestOutbound(outboundJSON, "https://www.gstatic.com/generate_204", "")
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.DelayMs = r.Delay
	res.StatusCode = r.StatusCode
	res.Error = r.Error
	res.OK = r.Success && r.StatusCode == 204
	// 面板里有人在测出站时会占用信号量，这不是节点故障，跳过本轮
	if !res.OK && strings.Contains(r.Error, "already running") {
		res.At = 0
	}
	if !res.OK && res.Error == "" {
		res.Error = fmt.Sprintf("HTTP %d", r.StatusCode)
	}
	return res
}

// RunNodeProbes 按 10 分钟节流，逐个节点做真实可用性实测。
func (s *InboundService) RunNodeProbes() {
	var inbounds []*model.Inbound
	if err := database.GetDB().Model(model.Inbound{}).Find(&inbounds).Error; err != nil {
		return
	}
	state := s.loadNodeProbeState()
	now := time.Now().Unix()
	changed := false

	for _, ib := range inbounds {
		if !ib.Enable {
			continue
		}
		if prev, ok := state[ib.Id]; ok && now-prev.At < int64(nodeProbeInterval.Seconds()) {
			continue
		}
		res := s.probeInboundUsability(ib)
		if res.At == 0 {
			// 被面板里的出站测试占用，保留上一轮结果
			continue
		}
		state[ib.Id] = res
		changed = true
	}

	alive := map[int]bool{}
	for _, ib := range inbounds {
		alive[ib.Id] = true
	}
	for id := range state {
		if !alive[id] {
			delete(state, id)
			changed = true
		}
	}
	if changed {
		s.saveNodeProbeState(state)
	}
}
