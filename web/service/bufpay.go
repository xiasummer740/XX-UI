package service

import (
	"crypto/md5"
	"encoding/hex"
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
func (s *BufPayService) doPay(endpoint string, form url.Values) (pay *BufPayPayment, apiErr error, transportErr error) {
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.PostForm(endpoint, form)
	if err != nil {
		return nil, nil, fmt.Errorf("请求 BufPay 失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, nil, fmt.Errorf("读取 BufPay 响应失败: %w", err)
	}

	result := &BufPayPayment{}
	if err := json.Unmarshal(body, result); err != nil {
		return nil, nil, fmt.Errorf("BufPay 返回了非预期内容: %s", strings.TrimSpace(string(body)))
	}
	if result.Status != "ok" {
		return nil, fmt.Errorf("bufpay 返回: %s", bufPayErrorText(result.Status)), nil
	}
	return result, nil, nil
}

// VerifyNotify 校验支付回调的签名。
func (s *BufPayService) VerifyNotify(secret, aoid, orderId, orderUid, price, payPrice, sign string) bool {
	secret = strings.TrimSpace(secret)
	if secret == "" || sign == "" {
		return false
	}
	expect := s.sign(aoid, orderId, orderUid, price, payPrice, secret)
	return strings.EqualFold(expect, sign)
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
