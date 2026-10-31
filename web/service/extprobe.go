package service

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/XiaSummer740/XX-UI/database"
	"github.com/XiaSummer740/XX-UI/database/model"
)

// 外部多点实测：从 check-host.net 的多个地区节点去连你的节点端口，
// 拿到「别人连你要多久」的真实数字。
//
// 服务端自己测自己永远是 0-2ms，对用户没有任何参考意义，
// 所以这里借外部探针节点来量真实网络路径。

const (
	extProbeStateKey = "externalProbeState"
	// 同一节点两次外部实测的最小间隔
	extProbeInterval = 15 * time.Minute
	// 每轮最多测几个节点（check-host 有速率限制，别打太狠）
	extProbePerRound = 4
	// 每个端口最多取几个探针节点（随机模式下用）
	extProbeMaxNodes = 6
)

// 优先使用离中国用户近的探针节点，数字才有参考价值
var extProbePreferredNodes = []string{
	"hk1.node.check-host.net",
	"jp1.node.check-host.net",
	"sg1.node.check-host.net",
	"in1.node.check-host.net",
	"vn1.node.check-host.net",
}

// ExtProbeNode 是单个外部探针节点的结果。
type ExtProbeNode struct {
	Node    string `json:"node"`
	Country string `json:"country"`
	Ms      int64  `json:"ms"`
	Err     string `json:"err,omitempty"`
}

// ExtProbe 是某个节点的外部实测汇总。
type ExtProbe struct {
	At    int64          `json:"at"`
	Nodes []ExtProbeNode `json:"nodes"`
	MinMs int64          `json:"minMs"`
	AvgMs int64          `json:"avgMs"`
	MaxMs int64          `json:"maxMs"`
	Fail  int            `json:"fail"`
	Host  string         `json:"host"`
}

func (s *InboundService) loadExtProbeState() map[int]ExtProbe {
	state := map[int]ExtProbe{}
	var ss SettingService
	raw, err := ss.getString(extProbeStateKey)
	if err != nil || raw == "" {
		return state
	}
	_ = json.Unmarshal([]byte(raw), &state)
	if state == nil {
		state = map[int]ExtProbe{}
	}
	return state
}

func (s *InboundService) saveExtProbeState(state map[int]ExtProbe) {
	bs, err := json.Marshal(state)
	if err != nil {
		return
	}
	var ss SettingService
	_ = ss.setString(extProbeStateKey, string(bs))
}

// ExtProbeSnapshot 供面板读取。
func (s *InboundService) ExtProbeSnapshot() map[int]ExtProbe {
	return s.loadExtProbeState()
}

func extProbeHTTP() *http.Client {
	return &http.Client{Timeout: 15 * time.Second}
}

// queryCheckHost 触发一次 check-tcp，并轮询结果。
func queryCheckHost(host string, port int) ([]ExtProbeNode, error) {
	client := extProbeHTTP()
	target := fmt.Sprintf("%s:%d", host, port)
	// 优先挑离中国用户近的亚洲探针节点；显式指定失败时退回随机节点
	createURL := fmt.Sprintf("https://check-host.net/check-tcp?host=%s&max_nodes=%d", target, extProbeMaxNodes)
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("https://check-host.net/check-tcp?host=%s", target))
	for _, n := range extProbePreferredNodes {
		sb.WriteString("&node=" + n)
	}
	createURL = sb.String()

	req, err := http.NewRequest(http.MethodGet, createURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "XX-UI external probe")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	var create struct {
		OK        int            `json:"ok"`
		RequestID string         `json:"request_id"`
		Nodes     map[string]any `json:"nodes"`
		Error     string         `json:"error"`
	}
	if err := json.Unmarshal(body, &create); err != nil {
		return nil, fmt.Errorf("解析 check-host 响应失败: %v", err)
	}
	if create.RequestID == "" {
		return nil, fmt.Errorf("check-host 未返回 request_id: %s", create.Error)
	}

	// 结果要几秒才齐，轮询几次把先到的收下来
	results := map[string]ExtProbeNode{}
	seen := map[string]map[string]any{}
	for attempt := 0; attempt < 5; attempt++ {
		time.Sleep(2500 * time.Millisecond)
		rreq, _ := http.NewRequest(http.MethodGet,
			"https://check-host.net/check-result/"+create.RequestID, nil)
		rreq.Header.Set("Accept", "application/json")
		rreq.Header.Set("User-Agent", "XX-UI external probe")
		rresp, err := client.Do(rreq)
		if err != nil {
			continue
		}
		rbody, _ := io.ReadAll(io.LimitReader(rresp.Body, 1<<20))
		_ = rresp.Body.Close()

		var raw map[string]any
		if err := json.Unmarshal(rbody, &raw); err != nil {
			continue
		}
		for nodeName, v := range raw {
			seen[nodeName] = map[string]any{"raw": v}
			item := ExtProbeNode{Node: nodeName}
			item.Country = extProbeCountry(create.Nodes, nodeName)
			switch data := v.(type) {
			case []any:
				if len(data) == 0 {
					continue // 还没出结果
				}
				if m, ok := data[0].(map[string]any); ok {
					if t, ok2 := m["time"].(float64); ok2 {
						item.Ms = int64(t * 1000)
					}
					if e, ok2 := m["error"].(string); ok2 {
						item.Err = e
					}
				}
			default:
				continue
			}
			results[nodeName] = item
		}
		if len(results) >= 3 {
			break
		}
	}

	out := make([]ExtProbeNode, 0, len(results))
	for _, item := range results {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ms < out[j].Ms })
	if len(out) == 0 {
		return nil, fmt.Errorf("check-host 没返回可用结果")
	}
	return out, nil
}

// extProbeCountry 从 create 响应里取国家代码。
func extProbeCountry(nodes map[string]any, nodeName string) string {
	if nodes == nil {
		return ""
	}
	raw, ok := nodes[nodeName]
	if !ok {
		return ""
	}
	if arr, ok2 := raw.([]any); ok2 && len(arr) > 1 {
		if s, ok3 := arr[1].(string); ok3 {
			return s
		}
	}
	return ""
}

// publicIPv4 返回本机公网 IPv4（取不到就返回空）。
func publicIPv4() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if v4 := ipnet.IP.To4(); v4 != nil && !v4.IsPrivate() {
				return v4.String()
			}
		}
	}
	return ""
}

// RunExternalProbes 每轮挑最久没测过的若干个节点做外部实测。
func (s *InboundService) RunExternalProbes() {
	host := publicIPv4()
	if host == "" {
		return
	}
	var inbounds []*model.Inbound
	if err := database.GetDB().Model(model.Inbound{}).Find(&inbounds).Error; err != nil {
		return
	}
	state := s.loadExtProbeState()
	now := time.Now().Unix()

	// 只测启用的入站，按「上次实测时间」从旧到新排序
	type cand struct {
		ib *model.Inbound
		at int64
	}
	var cands []cand
	for _, ib := range inbounds {
		if !ib.Enable {
			continue
		}
		at := state[ib.Id].At
		if now-at < int64(extProbeInterval.Seconds()) {
			continue
		}
		cands = append(cands, cand{ib: ib, at: at})
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].at < cands[j].at })

	for i, c := range cands {
		if i >= extProbePerRound {
			break
		}
		nodes, err := queryCheckHost(host, c.ib.Port)
		if err != nil {
			continue
		}
		probe := ExtProbe{At: now, Nodes: nodes, Host: fmt.Sprintf("%s:%d", host, c.ib.Port)}
		var sum int64
		var okCount int
		for _, n := range nodes {
			// 跨地区连一台海外 VPS 不可能 1-2ms，这种是探针自身抖动，剔掉
			if n.Err != "" || n.Ms < 3 {
				probe.Fail++
				continue
			}
			okCount++
			sum += n.Ms
			if probe.MinMs == 0 || n.Ms < probe.MinMs {
				probe.MinMs = n.Ms
			}
			if n.Ms > probe.MaxMs {
				probe.MaxMs = n.Ms
			}
		}
		if okCount > 0 {
			probe.AvgMs = sum / int64(okCount)
			state[c.ib.Id] = probe
		}
	}
	s.saveExtProbeState(state)
}

// probeStateView 是 /panel/api/inbounds/probeState 的返回结构。
type probeStateView struct {
	Probes map[int]NodeProbeResult `json:"probes"`
	Ext    map[int]ExtProbe        `json:"ext"`
	// 外部探针是否可用（拿到过数据）
	ExtEnabled bool `json:"extEnabled"`
}

// ProbeState 汇总内部实测与外部实测。
func (s *InboundService) ProbeState() probeStateView {
	ext := s.ExtProbeSnapshot()
	return probeStateView{
		Probes:     s.NodeProbeSnapshot(),
		Ext:        ext,
		ExtEnabled: strings.TrimSpace(publicIPv4()) != "" && len(ext) > 0,
	}
}
