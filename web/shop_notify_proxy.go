package web

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/XiaSummer740/XX-UI/logger"
	"github.com/XiaSummer740/XX-UI/web/service"
)

// startShopNotifyProxy 在标准端口（默认 80）上开一个纯 HTTP 回调入口，
// 把支付平台的回调原样转给面板自己的 HTTPS 回调路由。
//
// 为什么需要它：有些支付平台（比如 BufPay）的回调服务器只肯往 80/443 发，
// 面板跑在 2053 这种非标准端口上时，对方压根连不上，订单永远停在待确认。
func (s *Server) startShopNotifyProxy() {
	var shop service.ShopService
	port := shop.GetNotifyHTTPPort()
	if port <= 0 {
		return
	}

	webPort, err := s.settingService.GetPort()
	if err != nil || webPort <= 0 {
		return
	}
	basePath, _ := s.settingService.GetBasePath()
	basePath = "/" + strings.Trim(basePath, "/")
	notifyPath := basePath + "/panel/api/shop/bufpay/notify"

	client := &http.Client{
		Timeout: 25 * time.Second,
		Transport: &http.Transport{
			// 本机回环转发，证书主体对不上也无所谓
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimSuffix(r.URL.Path, "/") != strings.TrimSuffix(notifyPath, "/") {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		target := fmt.Sprintf("https://127.0.0.1:%d%s", webPort, notifyPath)
		req, perr := http.NewRequest(http.MethodPost, target, bytes.NewReader(body))
		if perr != nil {
			http.Error(w, "fail", http.StatusInternalServerError)
			return
		}
		if ct := r.Header.Get("Content-Type"); ct != "" {
			req.Header.Set("Content-Type", ct)
		}
		req.Header.Set("User-Agent", "shop-notify-proxy")

		resp, derr := client.Do(req)
		if derr != nil {
			logger.Warning("[shop] 回调转发失败: ", derr)
			http.Error(w, "fail", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		out, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		logger.Info("[shop] 标准端口收到回调并转发，结果: ", resp.StatusCode, " ", strings.TrimSpace(string(out)))
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(out)
	})

	ln, lerr := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if lerr != nil {
		logger.Warning("[shop] 无法在 ", port, " 端口开启回调兜底入口（可能被占用）: ", lerr)
		return
	}
	srv := &http.Server{
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
	go func() {
		_ = srv.Serve(ln)
	}()
	logger.Info("[shop] 回调兜底入口已启动: http://0.0.0.0:", port, notifyPath)
}
