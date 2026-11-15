package service

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/XiaSummer740/XX-UI/logger"
)

// 自建第四方收款（项目见 F:\summer\vs-code\第四方收款）。
// 协议：POST /api/create 下单、GET /api/query 查单、POST 回调（MD5 签名）。
// 钱进的是你自己的支付宝/微信静态码，落地没有任何第三方抽成或挂机依赖。
type SelfPayService struct{}

type SelfPayConfig struct {
	API string // 例如 http://127.0.0.1:8088
	PID string
	Key string
	Way string // alipay / wxpay
}

func (s *ShopService) GetSelfPayConfig() SelfPayConfig {
	get := func(k string) string {
		v, _ := s.settingService.getString(k)
		return strings.TrimSpace(v)
	}
	way := get("shopSelfPayWay")
	if way == "" {
		way = "alipay"
	}
	return SelfPayConfig{
		API: strings.TrimRight(get("shopSelfPayApi"), "/"),
		PID: get("shopSelfPayPid"),
		Key: get("shopSelfPayKey"),
		Way: way,
	}
}

// Create 在自建网关上开一笔单，返回收款页地址（机器人把它画成二维码）。
func (s *SelfPayService) Create(cfg SelfPayConfig, name string, price float64, outTradeNo, notifyURL string) (string, string, error) {
	if cfg.API == "" || cfg.PID == "" || cfg.Key == "" {
		return "", "", fmt.Errorf("自建收款还没配置完整（网关地址 / 商户号 / 密钥）")
	}
	form := url.Values{}
	form.Set("pid", cfg.PID)
	form.Set("key", cfg.Key)
	form.Set("out_trade_no", outTradeNo)
	form.Set("money", fmt.Sprintf("%.2f", price))
	form.Set("name", name)
	form.Set("type", cfg.Way)
	form.Set("notify_url", notifyURL)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.PostForm(cfg.API+"/api/create", form)
	if err != nil {
		return "", "", fmt.Errorf("请求自建收款失败: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var out struct {
		Code    int    `json:"code"`
		Msg     string `json:"msg"`
		TradeNo string `json:"trade_no"`
		PayURL  string `json:"pay_url"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", "", fmt.Errorf("自建收款返回非 JSON: %s", trimForLog(string(body), 120))
	}
	if out.Code != 1 || out.PayURL == "" {
		return "", "", fmt.Errorf("自建收款下单失败：%s", out.Msg)
	}
	logger.Info("[shop] 自建收款下单成功 out=", outTradeNo, " trade=", out.TradeNo)
	return out.PayURL, out.TradeNo, nil
}

// Query 查单，返回是否已付款。
func (s *SelfPayService) Query(cfg SelfPayConfig, outTradeNo string) (bool, string, error) {
	if cfg.API == "" || cfg.PID == "" || cfg.Key == "" {
		return false, "", fmt.Errorf("自建收款未配置")
	}
	u := fmt.Sprintf("%s/api/query?pid=%s&key=%s&out_trade_no=%s",
		cfg.API, url.QueryEscape(cfg.PID), url.QueryEscape(cfg.Key), url.QueryEscape(outTradeNo))
	client := &http.Client{Timeout: 12 * time.Second}
	resp, err := client.Get(u)
	if err != nil {
		return false, "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var out struct {
		Code   int `json:"code"`
		Status int `json:"status"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return false, "", fmt.Errorf("查单返回非 JSON: %s", trimForLog(string(body), 120))
	}
	if out.Status == 1 {
		return true, "payed", nil
	}
	return false, "wait", nil
}

// VerifyNotify 校验自建网关回调签名（md5(排序参数 + key)）。
func (s *SelfPayService) VerifyNotify(cfg SelfPayConfig, form map[string]string) (bool, error) {
	sign := form["sign"]
	if sign == "" {
		return false, fmt.Errorf("回调缺少签名")
	}
	want := signParamsMD5(form, cfg.Key)
	if !strings.EqualFold(want, sign) {
		return false, fmt.Errorf("回调签名不匹配")
	}
	if strings.EqualFold(form["status"], "paid") {
		return true, nil
	}
	return false, nil
}

// signParamsMD5 与网关侧保持一致的签名算法。
func signParamsMD5(params map[string]string, key string) string {
	keys := make([]string, 0, len(params))
	for k, v := range params {
		if k == "sign" || k == "sign_type" || v == "" {
			continue
		}
		keys = append(keys, k)
	}
	// 冒泡排序即可，参数很少
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
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

// 兼容：把字符串金额转成浮点（回调里字段可能是 "15.00"）
func parsePrice(v string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(v), 64)
	return f
}
