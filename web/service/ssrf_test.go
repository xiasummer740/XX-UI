package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/XiaSummer740/XX-UI/web/middleware"

	"github.com/gin-gonic/gin"
)

func TestCheckOutboundURLBlocksInternalTargets(t *testing.T) {
	ctx := context.Background()

	blocked := []string{
		"http://127.0.0.1:2053/panel/",
		"http://169.254.169.254/latest/meta-data/", // cloud metadata
		"http://10.1.2.3/",
		"http://192.168.1.1/",
		"http://[::1]:2053/",
	}
	for _, raw := range blocked {
		if err := CheckOutboundURL(ctx, raw); err == nil {
			t.Errorf("%s should have been refused", raw)
		}
	}

	invalid := []string{
		"ftp://example.com/file",
		"file:///etc/passwd",
		"not a url at all",
		"",
	}
	for _, raw := range invalid {
		if err := CheckOutboundURL(ctx, raw); err == nil {
			t.Errorf("%q should have been refused", raw)
		}
	}
}

func TestSameOrigin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	initTestLogger(t)

	cases := []struct {
		name   string
		method string
		origin string
		host   string
		want   int
	}{
		{"no origin is a non-browser client", http.MethodPost, "", "panel.example:2053", http.StatusOK},
		{"same origin", http.MethodPost, "https://panel.example:2053", "panel.example:2053", http.StatusOK},
		{"referer fallback", http.MethodPost, "https://panel.example:2053/panel/", "panel.example:2053", http.StatusOK},
		{"cross site post", http.MethodPost, "https://evil.example", "panel.example:2053", http.StatusForbidden},
		{"cross site delete", http.MethodDelete, "https://evil.example", "panel.example:2053", http.StatusForbidden},
		{"get is never blocked", http.MethodGet, "https://evil.example", "panel.example:2053", http.StatusOK},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			engine := gin.New()
			engine.Use(middleware.SameOrigin())
			engine.Any("/*path", func(c *gin.Context) { c.Status(http.StatusOK) })

			req := httptest.NewRequest(tc.method, "http://"+tc.host+"/panel/api/test", nil)
			req.Host = tc.host
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			rec := httptest.NewRecorder()
			engine.ServeHTTP(rec, req)

			if rec.Code != tc.want {
				t.Fatalf("want %d, got %d", tc.want, rec.Code)
			}
		})
	}
}
