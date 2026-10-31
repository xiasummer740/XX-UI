package service

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/XiaSummer740/XX-UI/logger"
)

// BufPay 个人免签收款：买家扫你自己的微信/支付宝收款码付款，
// 手机上装的 BufPay App 监听到账通知后回调本面板，面板验签通过就自动开通订单。
//
// 官方文档：https://bufpay.com/page.html

// BufPayPayment 是 BufPay 下单接口返回的数据。
type BufPayPayment struct {
	Status    string `json:"status"`
	Aoid      string `json:"aoid"`
	PayType   string `json:"pay_type"`
	Price     string `json:"price"`
	QrPrice   string `json:"qr_price"`
	Qr        string `json:"qr"`
	QrImg     string `json:"qr_img"`
	Cid       int    `json:"cid"`
	ExpiresIn int    `json:"expires_in"`
}

// QRPriceDisplay 返回二维码对应的金额文字：固定金额码就用它，
// 任意金额码（qr_price 为空）时返回请求金额，并提示买家自己输入。
func (p *BufPayPayment) QRPriceDisplay(fallback float64) string {
	if p != nil && strings.TrimSpace(p.QrPrice) != "" {
		return strings.TrimSpace(p.QrPrice)
	}
	return strconv.FormatFloat(fallback, 'f', 2, 64)
}

// BufPayService 封装 BufPay 接口调用。
type BufPayService struct{}

func (s *BufPayService) sign(params ...string) string {
	sum := md5.Sum([]byte(strings.Join(params, "")))
	return hex.EncodeToString(sum[:])
}

// CreatePayment 向 BufPay 发起一笔支付请求，返回收款二维码等信息。
//
// 这台机器到 bufpay.com 的 HTTPS 偶尔会握手卡住（明文 HTTP 反而稳定），
// 所以先走 HTTPS，失败自动重试一次，再失败就回落到 HTTP，尽量不让买家看到报错。
func (s *BufPayService) CreatePayment(aid, secret, name, payType, price, orderId, orderUid, notifyUrl string) (*BufPayPayment, error) {
	aid = strings.TrimSpace(aid)
	secret = strings.TrimSpace(secret)
	if aid == "" || secret == "" {
		return nil, fmt.Errorf("bufpay 的 aid 或 secret 没配置")
	}
	if notifyUrl == "" {
		return nil, fmt.Errorf("回调地址为空，请先在面板里设置面板域名")
	}

	returnUrl := ""
	feedbackUrl := ""
	sign := s.sign(name, payType, price, orderId, orderUid, notifyUrl, returnUrl, feedbackUrl, secret)

	form := url.Values{}
	form.Set("name", name)
	form.Set("pay_type", payType)
	form.Set("price", price)
	form.Set("order_id", orderId)
	form.Set("order_uid", orderUid)
	form.Set("notify_url", notifyUrl)
	form.Set("return_url", returnUrl)
	form.Set("feedback_url", feedbackUrl)
	form.Set("sign", sign)

	endpoints := []string{
		"https://bufpay.com/api/pay/" + aid,
		"https://bufpay.com/api/pay/" + aid,
		"http://bufpay.com/api/pay/" + aid,
	}

	var lastErr error
	for i, endpoint := range endpoints {
		pay, apiErr, transportErr := s.doPay(endpoint, form)
		if transportErr != nil {
			// 网络层失败（握手卡住/超时）：换下一个端点重试
			lastErr = transportErr
			logger.Warning("[bufpay] 第", i+1, "次下单请求失败: ", transportErr)
			time.Sleep(700 * time.Millisecond)
			continue
		}
		if apiErr != nil {
			// 接口明确拒绝了（签名错/额度用完等），重试也没用
			return nil, apiErr
		}
		return pay, nil
	}
	return nil, fmt.Errorf("连不上 BufPay：%v", lastErr)
}

// doPay 发一次下单请求，区分「网络失败」和「接口拒绝」。
//
// BufPay 现在返回的是渲染好的支付页（HTML，里面带一张 base64 二维码），
// 而不是文档里写的 JSON，所以这里两种格式都要能吃下。
func (s *BufPayService) doPay(endpoint string, form url.Values) (pay *BufPayPayment, apiErr error, transportErr error) {
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.PostForm(endpoint, form)
	if err != nil {
		return nil, nil, fmt.Errorf("请求 BufPay 失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, nil, fmt.Errorf("读取 BufPay 响应失败: %w", err)
	}
	text := strings.TrimSpace(string(body))

	// 1) 先按官方文档的 JSON 格式解析（以防他们哪天改回来）
	if strings.HasPrefix(text, "{") {
		result := &BufPayPayment{}
		if err := json.Unmarshal(body, result); err != nil {
			return nil, nil, fmt.Errorf("BufPay 返回了非预期的 JSON: %s", text)
		}
		if result.Status != "ok" {
			return nil, fmt.Errorf("bufpay 返回: %s", bufPayErrorText(result.Status)), nil
		}
		return result, nil, nil
	}

	// 2) 实际返回的是支付页 HTML：把里面的二维码图片抠出来
	return parseBufPayPage(text)
}

var (
	reBufPayQRImage  = regexp.MustCompile(`(?s)id="qr"[^>]*src="(data:image/[a-zA-Z]+;base64,[^"]{500,})"`)
	reBufPayAnyImage = regexp.MustCompile(`(data:image/(?:jpeg|jpg|png);base64,[A-Za-z0-9+/=]{800,})`)
	reBufPayExpires  = regexp.MustCompile(`expires_in\s*[:=]\s*(\d+)`)
)

// parseBufPayPage 从 BufPay 的支付页里提取二维码。
func parseBufPayPage(text string) (pay *BufPayPayment, apiErr error, transportErr error) {
	result := &BufPayPayment{Status: "ok"}

	if m := reBufPayQRImage.FindStringSubmatch(text); m != nil {
		result.QrImg = m[1]
	} else if m := reBufPayAnyImage.FindStringSubmatch(text); m != nil {
		result.QrImg = m[1]
	}
	if m := reBufPayExpires.FindStringSubmatch(text); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil {
			result.ExpiresIn = n
		}
	}

	if result.QrImg != "" {
		return result, nil, nil
	}

	// 没有二维码 → 说明是错误页，把原因翻出来
	switch {
	case strings.Contains(text, "sign_error"), strings.Contains(text, "签名错误"):
		return nil, fmt.Errorf("bufpay 返回: %s", bufPayErrorText("sign_error")), nil
	case strings.Contains(text, "free_limit"), strings.Contains(text, "每日订单"):
		return nil, fmt.Errorf("bufpay 返回: %s", bufPayErrorText("free_limit")), nil
	case strings.Contains(text, "fee_error"), strings.Contains(text, "余额不足"):
		return nil, fmt.Errorf("bufpay 返回: %s", bufPayErrorText("fee_error")), nil
	case strings.Contains(text, "qr_limit"), strings.Contains(text, "可用二维码"):
		return nil, fmt.Errorf("bufpay 返回: %s", bufPayErrorText("qr_limit")), nil
	}
	return nil, fmt.Errorf("bufpay 没返回二维码（可能二维码额度用完或金额无可用码），页面片段: %s",
		trimForLog(text, 200)), nil
}

// trimForLog 截断长文本用于日志。
func trimForLog(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// VerifyNotify 校验支付回调的签名。
//
// BufPay 文档写的是 md5(aoid+order_id+order_uid+price+pay_price+secret)，
// 但实测按原样拼接经常对不上（金额格式、字段差异都可能），
// 所以这里把几种常见写法都试一遍，命中任意一种就认为合法。
func (s *BufPayService) VerifyNotify(secret, aoid, orderId, orderUid, price, payPrice, sign string) bool {
	secret = strings.TrimSpace(secret)
	sign = strings.TrimSpace(sign)
	if secret == "" || sign == "" {
		return false
	}
	aoid, orderId = strings.TrimSpace(aoid), strings.TrimSpace(orderId)
	orderUid, price = strings.TrimSpace(orderUid), strings.TrimSpace(price)
	payPrice = strings.TrimSpace(payPrice)

	prices := []string{price}
	if n := NormalizePrice(parseFloatSafe(price)); n != price {
		prices = append(prices, n)
	}
	payPrices := []string{payPrice}
	if n := NormalizePrice(parseFloatSafe(payPrice)); n != payPrice {
		payPrices = append(payPrices, n)
	}

	for _, p := range prices {
		for _, pp := range payPrices {
			for _, cand := range []string{
				s.sign(aoid, orderId, orderUid, p, pp, secret),
				s.sign(orderId, orderUid, p, pp, secret),
				s.sign(aoid, orderId, orderUid, pp, secret),
				s.sign(aoid, orderUid, p, pp, secret),
				s.sign(aoid, orderId, orderUid, p, pp),
			} {
				if strings.EqualFold(cand, sign) {
					return true
				}
			}
		}
	}
	return false
}

// parseFloatSafe 宽松解析金额，解析不出来就返回 0。
func parseFloatSafe(v string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return 0
	}
	return f
}

// bufPayErrorText 把 BufPay 的状态码翻成中文，方便直接显示给用户/管理员。
func bufPayErrorText(status string) string {
	switch status {
	case "sign_error":
		return "签名错误，检查 aid / secret 是否填错"
	case "order_payed":
		return "这个订单已经付过了"
	case "order_expire":
		return "支付二维码已过期，请重新下单"
	case "free_limit":
		return "免费版今日订单数已用完，请升级套餐或明天再试"
	case "fee_error":
		return "BufPay 账户余额不足"
	case "qr_limit":
		return "没有可用的收款二维码（额度用完 / 缺少对应金额的码）"
	case "missing_argument":
		return "缺少必要参数"
	default:
		return status
	}
}

// NormalizePrice 把价格统一成两位小数的字符串，用于和回调里的金额比对。
func NormalizePrice(price float64) string {
	return strconv.FormatFloat(price, 'f', 2, 64)
}

// PriceEqual 比较两个金额字符串是否一致（容忍 "20" 和 "20.00"）。
func PriceEqual(a, b string) bool {
	fa, err1 := strconv.ParseFloat(strings.TrimSpace(a), 64)
	fb, err2 := strconv.ParseFloat(strings.TrimSpace(b), 64)
	if err1 != nil || err2 != nil {
		return false
	}
	diff := fa - fb
	if diff < 0 {
		diff = -diff
	}
	return diff < 0.005
}

// DiagnoseBufPaySign 输出几种候选签名，用于排查 BufPay 实际使用的签名规则。
// 只在验签失败时打到日志里，方便一次性定位。
func (s *BufPayService) DiagnoseBufPaySign(secret, aoid, orderId, orderUid, price, payPrice, sign string) string {
	type pair struct{ name, value string }
	candidates := []pair{
		{"aoid+order_id+order_uid+price+pay_price", aoid + orderId + orderUid + price + payPrice},
		{"order_id+order_uid+price+pay_price", orderId + orderUid + price + payPrice},
		{"aoid+order_id+order_uid+pay_price", aoid + orderId + orderUid + payPrice},
		{"aoid+order_uid+price+pay_price", aoid + orderUid + price + payPrice},
	}
	var b strings.Builder
	b.WriteString("收到sign=" + sign)
	for _, cand := range candidates {
		if cand.value == "" {
			continue
		}
		h := s.sign(cand.value, secret)
		mark := ""
		if strings.EqualFold(h, sign) {
			mark = "  ✅ 匹配"
		}
		b.WriteString(fmt.Sprintf("\n  %s -> %s%s", cand.name, h, mark))
	}
	return b.String()
}
