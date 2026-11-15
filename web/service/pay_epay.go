package service

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/XiaSummer740/XX-UI/logger"
)

// 易支付（彩虹易支付 / 大多数第四方聚合）通用接入。
// 协议是公开的：mapi.php 下单、api.php?act=order 查单、异步 notify 回调 + MD5 签名。
// 适合「有聚合支付商户号但没有营业执照」的场景，比 BufPay 的挂机监控可靠：
// 到账由支付通道主动回调，不依赖任何手机挂机。
type EpayService struct{}

// EpayConfig 是面板里填的商户配置。
type EpayConfig struct {
	API     string // 接口地址，例如 https://pay.example.com
	PID     string
	Key     string
	PayType string // alipay / wxpay
}

func (s *ShopService) GetEpayConfig() EpayConfig {
	get := func(k string) string {
		v, _ := s.settingService.getString(k)
		return strings.TrimSpace(v)
	}
	api := strings.TrimRight(get("shopEpayApi"), "/")
	pt := get("shopEpayType")
	if pt == "" {
		pt = "alipay"
	}
	return EpayConfig{API: api, PID: get("shopEpayPid"), Key: get("shopEpayKey"), PayType: pt}
}

// PayProvider 返回当前使用的支付通道：bufpay（默认）/ epay。
func (s *ShopService) PayProvider() string {
	v, _ := s.settingService.getString("shopPayProvider")
	v = strings.TrimSpace(strings.ToLower(v))
	if v == "" {
		return "bufpay"
	}
	return v
}

// sign 计算易支付签名：参数按 key 升序拼 k=v&...，去掉 sign/sign_type 与空值，末尾拼 key 再 md5。
func (e *EpayService) sign(params map[string]string, key string) string {
	keys := make([]string, 0, len(params))
	for k, v := range params {
		if k == "sign" || k == "sign_type" || v == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteString("&")
		}
		b.WriteString(k + "=" + params[k])
	}
	b.WriteString(key)
	return md5Hex(b.String())
}

// Create 下单，返回支付跳转地址（前端/机器人可把它画成二维码）。
func (e *EpayService) Create(cfg EpayConfig, name string, price float64, outTradeNo, notifyURL string) (string, string, error) {
	if cfg.API == "" || cfg.PID == "" || cfg.Key == "" {
		return "", "", fmt.Errorf("易支付还没配置完整（接口地址 / 商户ID / 密钥）")
	}
	params := map[string]string{
		"pid":          cfg.PID,
		"type":         cfg.PayType,
		"out_trade_no": outTradeNo,
		"notify_url":   notifyURL,
		"return_url":   notifyURL,
		"name":         name,
		"money":        fmt.Sprintf("%.2f", price),
		"sign_type":    "MD5",
	}
	params["sign"] = e.sign(params, cfg.Key)
	form := url.Values{}
	for k, v := range params {
		form.Set(k, v)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.PostForm(cfg.API+"/mapi.php", form)
	if err != nil {
		return "", "", fmt.Errorf("请求支付接口失败: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var out struct {
		Code    int    `json:"code"`
		Msg     string `json:"msg"`
		TradeNo string `json:"trade_no"`
		PayURL  string `json:"payurl"`
		QRCode  string `json:"qrcode"`
		URL     string `json:"url"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", "", fmt.Errorf("支付接口返回非 JSON: %s", trimForLog(string(body), 120))
	}
	if out.Code != 1 {
		return "", "", fmt.Errorf("下单失败：%s", out.Msg)
	}
	payURL := out.PayURL
	if payURL == "" {
		payURL = out.QRCode
	}
	if payURL == "" {
		payURL = out.URL
	}
	if payURL == "" {
		return "", "", fmt.Errorf("支付接口没返回支付地址")
	}
	logger.Info("[shop] 易支付下单成功 #", outTradeNo, " trade_no=", out.TradeNo)
	return payURL, out.TradeNo, nil
}

// Query 主动查单：返回是否已付款。
func (e *EpayService) Query(cfg EpayConfig, outTradeNo string) (bool, string, error) {
	if cfg.API == "" || cfg.PID == "" || cfg.Key == "" {
		return false, "", fmt.Errorf("易支付未配置")
	}
	u := fmt.Sprintf("%s/api.php?act=order&pid=%s&key=%s&out_trade_no=%s",
		cfg.API, url.QueryEscape(cfg.PID), url.QueryEscape(cfg.Key), url.QueryEscape(outTradeNo))
	client := &http.Client{Timeout: 12 * time.Second}
	resp, err := client.Get(u)
	if err != nil {
		return false, "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var out struct {
		Code   int    `json:"code"`
		Status int    `json:"status"`
		Msg    string `json:"msg"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return false, "", fmt.Errorf("查单返回非 JSON: %s", trimForLog(string(body), 120))
	}
	// status=1 表示已支付
	if out.Status == 1 {
		return true, "payed", nil
	}
	return false, "wait", nil
}

// VerifyNotify 校验异步回调签名，返回是否已付款。
func (e *EpayService) VerifyNotify(cfg EpayConfig, form map[string]string) (bool, string, error) {
	sign := form["sign"]
	if sign == "" {
		return false, "", fmt.Errorf("回调缺少签名")
	}
	calc := e.sign(form, cfg.Key)
	if !strings.EqualFold(calc, sign) {
		return false, "", fmt.Errorf("回调签名不匹配")
	}
	status := form["trade_status"]
	if status == "" {
		status = form["status"]
	}
	paid := strings.EqualFold(status, "TRADE_SUCCESS") || status == "1"
	return paid, status, nil
}

// md5Hex 计算 md5 十六进制串。
func md5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return fmt.Sprintf("%x", sum)
}
