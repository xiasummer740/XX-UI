package service

import "testing"

func TestPickRealityPort(t *testing.T) {
	none := func(int) bool { return false }
	allBusy := func(int) bool { return true }

	// 443 空闲时应优先选 443
	if p, reason := pickRealityPort(none, map[int]bool{}); p != 443 {
		t.Errorf("空闲时应选 443，实际 %d (%s)", p, reason)
	}

	// 443 被系统占用 -> 退到 8443
	busy443 := func(port int) bool { return port == 443 }
	if p, _ := pickRealityPort(busy443, map[int]bool{}); p != 8443 {
		t.Errorf("443 被占用时应选 8443，实际 %d", p)
	}

	// 443 已被本面板的入站占用 -> 也要让开
	if p, _ := pickRealityPort(none, map[int]bool{443: true}); p != 8443 {
		t.Errorf("443 已被入站占用时应选 8443，实际 %d", p)
	}

	// 常用端口全被占用 -> 落到高位端口
	busyCommon := func(port int) bool {
		for _, c := range realityPortCandidates {
			if port == c {
				return true
			}
		}
		return false
	}
	p, _ := pickRealityPort(busyCommon, map[int]bool{})
	if p < 20000 || p >= 40000 {
		t.Errorf("常用端口都忙时应退到 20000-39999，实际 %d", p)
	}

	// 连高位端口都不可用 -> 返回 0，由调用方报错
	if p, _ := pickRealityPort(allBusy, map[int]bool{}); p != 0 {
		t.Errorf("全部端口不可用时应返回 0，实际 %d", p)
	}
}

func TestPickRealityTarget(t *testing.T) {
	// 没有可用目标
	if _, ok := pickRealityTarget([]RealityCandidate{{Ok: false}, {Ok: false}}); ok {
		t.Error("全部失败时不应选出目标")
	}

	// 普通目标延迟更低 -> 选它
	cands := []RealityCandidate{
		{ServerName: "www.samsung.com", Ok: true, LatencyMs: 40},
		{ServerName: "www.tesla.com", Ok: true, LatencyMs: 60},
	}
	if got, _ := pickRealityTarget(cands); got.ServerName != "www.samsung.com" {
		t.Errorf("应选延迟最低的 www.samsung.com，实际 %s", got.ServerName)
	}

	// 过热目标虽然快，但要让出 penalty 才可能被选
	cands = []RealityCandidate{
		{ServerName: "www.microsoft.com", Ok: true, LatencyMs: 30}, // 30+80=110
		{ServerName: "www.samsung.com", Ok: true, LatencyMs: 90},   // 90
	}
	if got, _ := pickRealityTarget(cands); got.ServerName != "www.samsung.com" {
		t.Errorf("过热目标应被降权，期望 www.samsung.com，实际 %s", got.ServerName)
	}

	// 过热目标明显更快时仍然选它
	cands = []RealityCandidate{
		{ServerName: "www.microsoft.com", Ok: true, LatencyMs: 10}, // 90
		{ServerName: "www.samsung.com", Ok: true, LatencyMs: 200},  // 200
	}
	if got, _ := pickRealityTarget(cands); got.ServerName != "www.microsoft.com" {
		t.Errorf("过热目标快很多时应仍被选中，实际 %s", got.ServerName)
	}

	// 失败的目标不参与排序
	cands = []RealityCandidate{
		{ServerName: "bad.example", Ok: false, LatencyMs: 1},
		{ServerName: "www.tesla.com", Ok: true, LatencyMs: 70},
	}
	if got, _ := pickRealityTarget(cands); got.ServerName != "www.tesla.com" {
		t.Errorf("失败目标应被忽略，实际 %s", got.ServerName)
	}
}
