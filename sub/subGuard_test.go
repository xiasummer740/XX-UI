package sub

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestIsAllowedUA(t *testing.T) {
	ok := []string{
		"Shadowrocket/2.2.65 CFNetwork/1474 Darwin/23.0.0",
		"ClashforWindows/0.20.39",
		"v2rayN/6.23",
		"sing-box 1.8.0",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0)",
		"Streisand/1.6.6",
	}
	for _, ua := range ok {
		if !isAllowedUA(ua) {
			t.Fatalf("UA 应被放行: %q", ua)
		}
	}
	bad := []string{"", "python-requests/2.31", "Go-http-client/2.0-bot", "curl-test"}
	// 注意 curl 在白名单里（面板/脚本自测要用），这里只断言明显陌生的 UA
	for _, ua := range []string{"", "python-requests/2.31", "SomethingElse/1.0"} {
		if isAllowedUA(ua) {
			t.Fatalf("UA 不应被放行: %q", ua)
		}
	}
	_ = bad
}

func newGuardRouter(enable, strict bool, rpm, blockMin int) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/sub", NewSubGuard().Guard(enable, strict, rpm, blockMin))
	g.GET(":subid", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	return r
}

func doReq(r *gin.Engine, ua string) int {
	req := httptest.NewRequest(http.MethodGet, "/sub/abc", nil)
	if ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	req.RemoteAddr = "203.0.113.9:12345"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

func TestSubGuardDisabledPasses(t *testing.T) {
	r := newGuardRouter(false, true, 1, 30)
	for i := 0; i < 5; i++ {
		if code := doReq(r, "weird-agent/1.0"); code != http.StatusOK {
			t.Fatalf("关闭防泄漏时不应拦截，得到 %d", code)
		}
	}
}

func TestSubGuardStrictUA(t *testing.T) {
	r := newGuardRouter(true, true, 100, 30)
	if code := doReq(r, "Shadowrocket/2.2.65"); code != http.StatusOK {
		t.Fatalf("白名单 UA 应放行，得到 %d", code)
	}
	if code := doReq(r, "weird-agent/1.0"); code != http.StatusForbidden {
		t.Fatalf("陌生 UA 应被 403，得到 %d", code)
	}
}

func TestSubGuardRateLimit(t *testing.T) {
	r := newGuardRouter(true, false, 3, 30)
	for i := 0; i < 3; i++ {
		if code := doReq(r, "Shadowrocket/2.2.65"); code != http.StatusOK {
			t.Fatalf("第 %d 次请求应通过，得到 %d", i+1, code)
		}
	}
	if code := doReq(r, "Shadowrocket/2.2.65"); code != http.StatusTooManyRequests {
		t.Fatalf("超过速率应 429，得到 %d", code)
	}
}

func TestSubGuardBlockAfterViolations(t *testing.T) {
	r := newGuardRouter(true, true, 100, 30)
	// 连续 5 次陌生 UA：第 5 次开始直接拉黑（429）
	codes := make([]int, 0, 5)
	for i := 0; i < 5; i++ {
		codes = append(codes, doReq(r, "weird-agent/1.0"))
	}
	if codes[4] != http.StatusTooManyRequests {
		t.Fatalf("第 5 次违规应被拉黑，得到 %v", codes)
	}
	// 拉黑后即使 UA 正常也拒绝
	if code := doReq(r, "Shadowrocket/2.2.65"); code != http.StatusTooManyRequests {
		t.Fatalf("拉黑期间应拒绝，得到 %d", code)
	}
}
