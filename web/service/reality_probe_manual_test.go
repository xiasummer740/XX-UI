package service

import (
	"os"
	"testing"
	"time"
)

// TestManualProbeRealityTargets 只在手动验证时运行（XXUI_PROBE=1），
// 会真实联网探测候选偷站目标，用于确认探测逻辑在目标机器上工作正常。
func TestManualProbeRealityTargets(t *testing.T) {
	if os.Getenv("XXUI_PROBE") == "" {
		t.Skip("设置 XXUI_PROBE=1 才会真实联网探测")
	}
	cands := probeRealityTargets(realityTargetPool, 3500*time.Millisecond)
	for _, c := range cands {
		t.Logf("%-24s ok=%-5v %4dms tls=%s alpn=%s %s", c.ServerName, c.Ok, c.LatencyMs, c.TLSVersion, c.ALPN, c.Reason)
	}
	pick, ok := pickRealityTarget(cands)
	if !ok {
		t.Fatalf("没有任何可用目标")
	}
	t.Logf("最终选择: %s (%dms, 过热=%v)", pick.ServerName, pick.LatencyMs, overusedTargets[pick.ServerName])
}
