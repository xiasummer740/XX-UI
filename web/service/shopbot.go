package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
	"github.com/skip2/go-qrcode"
)

// 流量商店的 Telegram 交互：
//
//	/buy                          → 列出套餐档位
//	shop_gb <gb>                  → 选周期
//	shop_cycle <gb> <cycle>       → 确认页（金额 + 付款说明 + 我已付款）
//	shop_paid <gb> <cycle>        → 生成订单，通知管理员
//	shop_ok <orderId>             → 管理员确认到账 → 自动开通
//	shop_no <orderId>             → 管理员拒绝
//
// 回调数据不走 encodeQuery（那是管理员专属的 hash 编码通道），
// 这样买家和管理员都能用同一套按钮。

const shopCallbackPrefix = "shop_"

// handleShopCallback 处理商店相关的回调；返回 true 表示已处理。
func (t *Tgbot) handleShopCallback(callbackQuery *telego.CallbackQuery, chatId int64, isAdmin bool) bool {
	data := callbackQuery.Data
	if !strings.HasPrefix(data, shopCallbackPrefix) {
		return false
	}
	shop := ShopService{}
	args := strings.Fields(strings.TrimPrefix(data, shopCallbackPrefix))
	if len(args) == 0 {
		return true
	}

	switch args[0] {
	case "gb":
		if len(args) < 2 {
			return true
		}
		gb, _ := strconv.Atoi(args[1])
		t.sendShopCyclePicker(chatId, gb)
	case "cycle":
		if len(args) < 3 {
			return true
		}
		gb, _ := strconv.Atoi(args[1])
		t.sendShopConfirm(chatId, gb, args[2])
	case "pay":
		if len(args) < 4 {
			return true
		}
		gb, _ := strconv.Atoi(args[1])
		t.sendShopPayQR(chatId, gb, args[2], args[3])
	case "paid":
		if len(args) < 3 {
			return true
		}
		gb, _ := strconv.Atoi(args[1])
		method := ""
		if len(args) >= 4 {
			method = args[3]
		}
		t.createShopOrder(chatId, &callbackQuery.From, gb, args[2], method)
	case "ok":
		if !isAdmin || len(args) < 2 {
			t.sendCallbackAnswerTgBot(callbackQuery.ID, "只有管理员能确认订单")
			return true
		}
		id, _ := strconv.Atoi(args[1])
		t.approveShopOrder(chatId, callbackQuery.From.ID, id)
	case "no":
		if !isAdmin || len(args) < 2 {
			t.sendCallbackAnswerTgBot(callbackQuery.ID, "只有管理员能处理订单")
			return true
		}
		id, _ := strconv.Atoi(args[1])
		t.rejectShopOrder(chatId, callbackQuery.From.ID, id)
	case "back":
		t.sendShopMenu(chatId)
	case "node_qr":
		t.sendShopNodeQR(chatId)
	case "clash":
		t.sendShopClash(chatId)
	case "orders":
		if isAdmin {
			t.sendShopOrders(chatId, &shop)
		}
	}
	_ = shop
	return true
}

// methodLine 把支付方式渲染成「　·　微信支付」这样的一段。
func methodLine(method string) string {
	if method == "" {
		return ""
	}
	return "　·　" + method
}

// sendShopMenu 是 /buy 的入口：列出所有档位。
func (t *Tgbot) sendShopMenu(chatId int64) {
	shop := ShopService{}
	enable, _ := shop.GetShopEnable()
	if !enable {
		t.SendMsgToTgbot(chatId, "🛒 商店暂未开放。")
		return
	}
	plans := shop.GetShopPlans()
	if len(plans) == 0 {
		t.SendMsgToTgbot(chatId, "🛒 暂时没有在售套餐，请稍后再来。")
		return
	}

	msg := "🎁 <b>挑一个适合你的套餐</b>  <i>(第 1 步 / 共 4 步)</i>\n\n"
	for _, p := range plans {
		msg += fmt.Sprintf("▫️ <b>%d GB</b>  ·  %s\n", p.Gb, planPriceText(p))
	}
	msg += "\n点下面的按钮选流量 👇"

	var buttons []telego.InlineKeyboardButton
	for _, p := range plans {
		buttons = append(buttons, tu.InlineKeyboardButton(fmt.Sprintf("%d GB", p.Gb)).
			WithCallbackData(fmt.Sprintf("shop_gb %d", p.Gb)))
	}
	cols := 2
	if len(buttons) < 2 {
		cols = 1
	}
	keyboard := tu.InlineKeyboardGrid(tu.InlineKeyboardCols(cols, buttons...))
	t.SendMsgToTgbot(chatId, msg, keyboard)
}

// sendShopCyclePicker 让买家选月付/季付/年付。
func (t *Tgbot) sendShopCyclePicker(chatId int64, gb int) {
	shop := ShopService{}
	plan, ok := shop.FindPlan(gb)
	if !ok {
		t.SendMsgToTgbot(chatId, "没有这个套餐。")
		return
	}
	var rows [][]telego.InlineKeyboardButton
	var row []telego.InlineKeyboardButton
	if plan.Month > 0 {
		row = append(row, tu.InlineKeyboardButton(fmt.Sprintf("月付 ¥%s", formatPrice(plan.Month))).
			WithCallbackData(fmt.Sprintf("shop_cycle %d %s", gb, cycleMonth)))
	}
	if plan.Quarter > 0 {
		row = append(row, tu.InlineKeyboardButton(fmt.Sprintf("季付 ¥%s", formatPrice(plan.Quarter))).
			WithCallbackData(fmt.Sprintf("shop_cycle %d %s", gb, cycleQuarter)))
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	if plan.Year > 0 {
		rows = append(rows, tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(fmt.Sprintf("年付 ¥%s", formatPrice(plan.Year))).
				WithCallbackData(fmt.Sprintf("shop_cycle %d %s", gb, cycleYear)),
		))
	}
	rows = append(rows, tu.InlineKeyboardRow(
		tu.InlineKeyboardButton("⬅️ 返回套餐").WithCallbackData("shop_back"),
	))
	t.SendMsgToTgbot(chatId, fmt.Sprintf("✨ 已选 <b>%d GB</b>\n\n<b>第 2 步</b>：选付款周期\n\n<i>季付、年付更划算，用得久就更省一点。</i>", gb), tu.InlineKeyboard(rows...))
}

// sendShopConfirm 第 3 步：确认订单并让买家选支付方式。
func (t *Tgbot) sendShopConfirm(chatId int64, gb int, cycle string) {
	shop := ShopService{}
	price, ok := shop.PriceOf(gb, cycle)
	if !ok {
		t.SendMsgToTgbot(chatId, "这个组合暂时不可购买，回套餐里重新选一个吧 🙏")
		return
	}
	days := cycleDays(cycle)
	msg := fmt.Sprintf("🧾 <b>订单确认</b>  <i>(第 3 步 / 共 4 步)</i>\n\n"+
		"套餐：<b>%d GB</b>\n"+
		"周期：%s（%d 天）\n"+
		"金额：<b>¥%s</b>\n",
		gb, cycleLabel(cycle), days, formatPrice(price))

	qrWechat := shop.GetShopQrURL()
	qrAlipay := shop.GetShopQrURL2()

	switch {
	case qrWechat != "" && qrAlipay != "":
		// 两个码都有 → 让买家自己选
		msg += "\n💳 用哪个付？点下面的按钮，我把收款码发给你："
		kb := tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton("💚 微信支付").WithCallbackData(fmt.Sprintf("shop_pay %d %s wechat", gb, cycle)),
				tu.InlineKeyboardButton("💙 支付宝").WithCallbackData(fmt.Sprintf("shop_pay %d %s alipay", gb, cycle)),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton("⬅️ 返回套餐").WithCallbackData("shop_back"),
			),
		)
		t.SendMsgToTgbot(chatId, msg, kb)
	case qrWechat != "":
		t.sendShopPayQR(chatId, gb, cycle, "wechat")
	case qrAlipay != "":
		t.sendShopPayQR(chatId, gb, cycle, "alipay")
	default:
		// 没配收款码 → 只发文字说明 + 我已付款
		note := shop.GetShopPayNote()
		if note != "" {
			msg += "\n" + note + "\n"
		}
		msg += "\n💛 付完点下面的「✅ 我已付款」，我看到就立刻给你开通。"
		kb := tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton("✅ 我已付款").WithCallbackData(fmt.Sprintf("shop_paid %d %s", gb, cycle)),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton("⬅️ 返回套餐").WithCallbackData("shop_back"),
			),
		)
		t.SendMsgToTgbot(chatId, msg, kb)
	}
}

// sendShopPayQR 第 4 步：发对应收款码 + 「我已付款」按钮。
func (t *Tgbot) sendShopPayQR(chatId int64, gb int, cycle string, method string) {
	shop := ShopService{}
	price, ok := shop.PriceOf(gb, cycle)
	if !ok {
		t.SendMsgToTgbot(chatId, "这个组合暂时不可购买，回套餐里重新选一个吧 🙏")
		return
	}

	qrWechat := shop.GetShopQrURL()
	qrAlipay := shop.GetShopQrURL2()

	qr := qrWechat
	label := "💚 <b>微信支付</b>"
	otherMethod, otherLabel, otherQR := "alipay", "🔄 换用支付宝", qrAlipay
	if method == "alipay" {
		qr = qrAlipay
		label = "💙 <b>支付宝</b>"
		otherMethod, otherLabel, otherQR = "wechat", "🔄 换用微信", qrWechat
	}

	caption := fmt.Sprintf("%s\n\n金额：<b>¥%s</b>　（%d GB · %s）\n\n"+
		"<b>第 4 步</b>：扫码付 ¥%s，付完点下面的「✅ 我已付款」👇",
		label, formatPrice(price), gb, cycleLabel(cycle), formatPrice(price))

	rows := [][]telego.InlineKeyboardButton{
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("✅ 我已付款").WithCallbackData(fmt.Sprintf("shop_paid %d %s %s", gb, cycle, method)),
		),
	}
	if otherQR != "" {
		rows = append(rows, tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(otherLabel).WithCallbackData(fmt.Sprintf("shop_pay %d %s %s", gb, cycle, otherMethod)),
		))
	}
	rows = append(rows, tu.InlineKeyboardRow(
		tu.InlineKeyboardButton("⬅️ 返回套餐").WithCallbackData("shop_back"),
	))

	if qr != "" {
		t.sendShopQrPhoto(chatId, qr, strings.Replace(caption, "<b>第 4 步</b>：", "", 1))
		t.SendMsgToTgbot(chatId, "☝️ 上面是收款码，付完点下面按钮就完成了 ✅", tu.InlineKeyboard(rows...))
		return
	}
	t.SendMsgToTgbot(chatId, caption, tu.InlineKeyboard(rows...))
}

// sendShopQrPhoto 发送收款码：既支持 http 图片地址，也支持面板里直接上传的 base64（data:image/...）。
func (t *Tgbot) sendShopQrPhoto(chatId int64, qr string, caption string) {
	if bot == nil || !isRunning {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	file := telego.InputFile{URL: qr}
	if strings.HasPrefix(qr, "data:image") {
		if idx := strings.Index(qr, "base64,"); idx >= 0 {
			data, derr := base64.StdEncoding.DecodeString(qr[idx+len("base64,"):])
			if derr == nil && len(data) > 0 {
				file = tu.FileFromBytes(data, "qrcode.png")
			}
		}
	}

	_, err := bot.SendPhoto(ctx, &telego.SendPhotoParams{
		ChatID:    tu.ID(chatId),
		Photo:     file,
		Caption:   caption,
		ParseMode: telego.ModeHTML,
	})
	if err != nil {
		// 发不出图片就退回文字，别让买家卡住
		t.SendMsgToTgbot(chatId, caption+"\n\n（收款码图片发送失败，请直接联系管理员）")
	}
}

// sendContentQR 把一段内容（订阅链接）画成二维码图片发给买家。
func (t *Tgbot) sendContentQR(chatId int64, content string, caption string) {
	if bot == nil || !isRunning || content == "" {
		return
	}
	png, err := qrcode.Encode(content, qrcode.Medium, 512)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, _ = bot.SendPhoto(ctx, &telego.SendPhotoParams{
		ChatID:    tu.ID(chatId),
		Photo:     tu.FileFromBytes(png, "sub-qr.png"),
		Caption:   caption,
		ParseMode: telego.ModeHTML,
	})
}

// createShopOrder 买家点「我已付款」：落单并通知管理员。
func (t *Tgbot) createShopOrder(chatId int64, from *telego.User, gb int, cycle string, payMethod string) {
	shop := ShopService{}
	name := from.FirstName
	if from.Username != "" {
		name += "(@" + from.Username + ")"
	}
	methodLabel := ""
	switch payMethod {
	case "wechat":
		methodLabel = "微信支付"
	case "alipay":
		methodLabel = "支付宝"
	}
	order, err := shop.CreateOrder(chatId, name, gb, cycle, methodLabel)
	if err != nil {
		t.SendMsgToTgbot(chatId, "❌ "+err.Error())
		return
	}

	t.SendMsgToTgbot(chatId, fmt.Sprintf(
		"💌 <b>收到啦，订单 #%d 已登记</b>  <i>(4/4 完成)</i>\n\n"+
			"套餐：<b>%d GB</b>　%s\n"+
			"金额：<b>¥%s</b>%s\n\n"+
			"我核对到账后马上开通，订阅链接和二维码会直接发到这里，你不用守着刷 ⏳\n"+
			"<i>一般几分钟内就好，夜里可能慢一点点。</i>",
		order.Id, order.PlanGb, cycleLabel(order.Cycle), formatPrice(order.Price), methodLine(order.Remark)))

	kb := tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("✅ 确认到账并开通").WithCallbackData(fmt.Sprintf("shop_ok %d", order.Id)),
			tu.InlineKeyboardButton("❌ 拒绝").WithCallbackData(fmt.Sprintf("shop_no %d", order.Id)),
		),
	)
	t.SendMsgToTgbotAdmins(fmt.Sprintf("🛒 <b>新订单来了</b>\n\n%s\n\n核对到账后点下面按钮 👇", orderText(order)), kb)
}

// approveShopOrder 管理员确认到账 → 开通客户端 → 把订阅链接发给买家。
func (t *Tgbot) approveShopOrder(chatId int64, adminId int64, orderId int) {
	shop := ShopService{}
	order, err := shop.ApproveOrder(orderId, adminId)
	if err != nil {
		t.SendMsgToTgbot(chatId, "❌ 开通失败："+err.Error())
		return
	}

	links, linkErr := t.fetchClientLinks(order.ClientEmail)
	expireAt := time.Now().AddDate(0, 0, order.Days).Format("2006-01-02")
	msg := fmt.Sprintf("🎉 <b>开通好啦，欢迎上车！</b>\n\n"+
		"套餐：<b>%d GB</b>　%s\n"+
		"有效期到：<b>%s</b>\n",
		order.PlanGb, cycleLabel(order.Cycle), expireAt)

	if linkErr == nil && len(links) > 0 {
		msg += "\n📱 <b>你的节点链接</b>（点一下复制到客户端）\n"
		msg += "<code>" + links[0] + "</code>"
		if len(links) > 1 {
			msg += fmt.Sprintf("\n\n<i>（这个订阅里一共 %d 个节点，下面按钮里有完整订阅）</i>", len(links))
		}
		msg += "\n\n⬇️ 下面还有一张<b>节点二维码</b>，小火箭/Clash Verge 等直接扫码就能加节点。\n" +
			"<i>用的时候有任何问题，直接回我一句就好 🌙</i>"
	} else {
		subURL, _, err := t.buildSubscriptionURLs(order.ClientEmail)
		if err == nil && subURL != "" {
			msg += "\n📎 <b>订阅链接</b>\n<code>" + subURL + "</code>"
		} else {
			msg += "\n（链接生成失败，请联系管理员手动获取）"
		}
	}

	kb := tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("📱 节点二维码").WithCallbackData("shop_node_qr"),
			tu.InlineKeyboardButton("🐱 Clash 订阅").WithCallbackData("shop_clash"),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("📶 我的流量").WithCallbackData("client_traffic"),
			tu.InlineKeyboardButton("🔗 订阅链接").WithCallbackData("client_sub_links"),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("🛒 再买一份").WithCallbackData("shop_back"),
		),
	)
	t.SendMsgToTgbot(order.ChatId, msg, kb)

	// 默认给单节点二维码（小火箭扫了直接加节点）
	if linkErr == nil && len(links) > 0 {
		t.sendContentQR(order.ChatId, links[0], "📱 这是你的<b>节点二维码</b>，用客户端扫码直接添加节点")
	}
	t.SendMsgToTgbot(chatId, fmt.Sprintf("✅ 订单 #%d 已开通（客户端 %s，%d GB/%s），节点和二维码已发给买家。",
		order.Id, order.ClientEmail, order.PlanGb, cycleLabel(order.Cycle)))
}

// rejectShopOrder 管理员拒绝订单。
func (t *Tgbot) rejectShopOrder(chatId int64, adminId int64, orderId int) {
	shop := ShopService{}
	order, err := shop.RejectOrder(orderId, adminId, "管理员拒绝")
	if err != nil {
		t.SendMsgToTgbot(chatId, "❌ "+err.Error())
		return
	}
	t.SendMsgToTgbot(chatId, fmt.Sprintf("已拒绝订单 #%d，买家会收到通知。", order.Id))
	t.SendMsgToTgbot(order.ChatId, fmt.Sprintf(
		"🥺 <b>订单 #%d 没能通过</b>\n\n"+
			"可能是没收到款，或者金额/备注对不上。\n"+
			"不用着急，直接回我一句说明情况，我们核对一下就补上 🙌", order.Id))
}

// sendShopOrders 管理员查看最近订单。
func (t *Tgbot) sendShopOrders(chatId int64, shop *ShopService) {
	orders, err := shop.ListOrders(20)
	if err != nil || len(orders) == 0 {
		t.SendMsgToTgbot(chatId, "还没有订单。")
		return
	}
	var sb strings.Builder
	var pendingCount, approvedCount int
	var revenue float64
	for _, o := range orders {
		switch o.Status {
		case orderPending:
			pendingCount++
		case orderApproved:
			approvedCount++
			revenue += o.Price
		}
	}
	sb.WriteString(fmt.Sprintf("📋 <b>最近 %d 单</b>\n待确认 <b>%d</b> 单 · 已开通 <b>%d</b> 单 · 合计 <b>¥%.2f</b>\n\n",
		len(orders), pendingCount, approvedCount, revenue))
	for _, o := range orders {
		icon := "⏳"
		if o.Status == orderApproved {
			icon = "✅"
		} else if o.Status == orderRejected {
			icon = "❌"
		}
		sb.WriteString(fmt.Sprintf("\n%s %s", icon, orderText(o)))
	}
	t.SendMsgToTgbot(chatId, sb.String())
}

// fetchClientLinks 拉取订阅内容并拆成单节点链接（vless://... 等）。
func (t *Tgbot) fetchClientLinks(email string) ([]string, error) {
	subURL, _, err := t.buildSubscriptionURLs(email)
	if err != nil || subURL == "" {
		return nil, fmt.Errorf("订阅地址不可用")
	}
	req, err := http.NewRequest("GET", subURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/plain, */*;q=0.1")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := optimizedHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	content := string(body)
	if encoded, _ := t.settingService.GetSubEncrypt(); encoded {
		if decoded, derr := base64.StdEncoding.DecodeString(strings.TrimSpace(content)); derr == nil {
			content = string(decoded)
		}
	}

	var links []string
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "://") && !strings.HasPrefix(line, "http") {
			links = append(links, line)
		}
	}
	if len(links) == 0 {
		return nil, fmt.Errorf("没有解析到节点")
	}
	return links, nil
}

// buildClashURL 生成 Clash/Mihomo 订阅地址（面板里 /clash/ 路径）。
func (t *Tgbot) buildClashURL(email string) (string, error) {
	if enable, _ := t.settingService.GetSubClashEnable(); !enable {
		return "", fmt.Errorf("clash 订阅未开启")
	}
	traffic, client, err := t.inboundService.GetClientByEmail(email)
	_ = traffic
	if err != nil || client == nil {
		return "", fmt.Errorf("client not found")
	}

	subURL, _, err := t.buildSubscriptionURLs(email)
	if err != nil || subURL == "" {
		return "", fmt.Errorf("订阅地址不可用")
	}
	// 把 /sub/<token> 换成 /clash/<token>，域名与端口保持一致
	clashPath, _ := t.settingService.GetSubClashPath()
	subPath, _ := t.settingService.GetSubPath()
	clashPath = "/" + strings.Trim(clashPath, "/") + "/"
	if subPath = "/" + strings.Trim(subPath, "/") + "/"; subPath != "/" && strings.Contains(subURL, subPath) {
		return strings.Replace(subURL, subPath, clashPath, 1), nil
	}
	// 兜底：按 scheme://host 重拼
	idx := strings.Index(subURL, "://")
	if idx < 0 {
		return "", fmt.Errorf("订阅地址异常")
	}
	rest := subURL[idx+3:]
	slash := strings.Index(rest, "/")
	if slash < 0 {
		return "", fmt.Errorf("订阅地址异常")
	}
	return subURL[:idx+3] + rest[:slash] + clashPath + rest[strings.LastIndex(rest, "/")+1:], nil
}

// clientEmailByChatId 按 Telegram 用户 ID 找到他的客户端 email。
func (t *Tgbot) clientEmailByChatId(chatId int64) string {
	traffics, err := t.inboundService.GetClientTrafficTgBot(chatId)
	if err != nil || len(traffics) == 0 {
		return ""
	}
	return traffics[0].Email
}

// sendShopNodeQR 发送单节点链接 + 节点二维码。
func (t *Tgbot) sendShopNodeQR(chatId int64) {
	email := t.clientEmailByChatId(chatId)
	if email == "" {
		t.SendMsgToTgbot(chatId, "还没找到你的节点，可能是还没开通，或者已经在别处登录过 🥲")
		return
	}
	links, err := t.fetchClientLinks(email)
	if err != nil || len(links) == 0 {
		t.SendMsgToTgbot(chatId, "节点暂时取不到，稍后再点一次，或者直接找我 🙌")
		return
	}
	msg := "📱 <b>你的节点链接</b>\n<code>" + links[0] + "</code>"
	if len(links) > 1 {
		msg += fmt.Sprintf("\n\n<i>订阅里一共有 %d 个节点，想要全部就用「🔗 订阅链接」。</i>", len(links))
	}
	t.SendMsgToTgbot(chatId, msg)
	t.sendContentQR(chatId, links[0], "📱 扫码直接添加节点（小火箭 / Clash Verge / v2rayN 都行）")
}

// sendShopClash 发送 Clash 订阅（链接 + 二维码）。
func (t *Tgbot) sendShopClash(chatId int64) {
	email := t.clientEmailByChatId(chatId)
	if email == "" {
		t.SendMsgToTgbot(chatId, "还没找到你的订阅，可能是还没开通 🥲")
		return
	}
	clashURL, err := t.buildClashURL(email)
	if err != nil || clashURL == "" {
		t.SendMsgToTgbot(chatId, "Clash 订阅没开启，先找管理员开一下 🙏")
		return
	}
	msg := "🐱 <b>Clash / Mihomo 订阅</b>\n<code>" + clashURL + "</code>\n\n" +
		"<i>把这条链接粘到 Clash Verge / Mihomo / ClashX 的「订阅」里，点更新就行。</i>\n" +
		"下面那张二维码也可以直接扫。"
	t.SendMsgToTgbot(chatId, msg)
	t.sendContentQR(chatId, clashURL, "🐱 Clash 订阅二维码，扫码导入订阅")
}

// sendShopStats 管理员经营看板（/stats）。
func (t *Tgbot) sendShopStats(chatId int64) {
	shop := ShopService{}
	stats, err := shop.GetShopStats()
	if err != nil {
		t.SendMsgToTgbot(chatId, "❌ 统计读取失败："+err.Error())
		return
	}

	var sb strings.Builder
	sb.WriteString("📊 <b>经营看板</b>\n\n")
	sb.WriteString(fmt.Sprintf("今日：<b>¥%.2f</b>（%d 单）\n", stats.TodayRevenue, stats.TodayOrders))
	sb.WriteString(fmt.Sprintf("本月：<b>¥%.2f</b>（%d 单）\n", stats.MonthRevenue, stats.MonthOrders))
	sb.WriteString(fmt.Sprintf("累计：<b>¥%.2f</b>（%d 单已开通）\n\n", stats.TotalRevenue, stats.Approved))

	sb.WriteString(fmt.Sprintf("⏳ 待确认：<b>%d</b> 单\n", stats.Pending))
	sb.WriteString(fmt.Sprintf("❌ 已拒绝：%d 单\n", stats.Rejected))
	sb.WriteString(fmt.Sprintf("👥 买家：<b>%d</b> 人\n", stats.Buyers))
	sb.WriteString(fmt.Sprintf("🖥 在售客户端：<b>%d</b> 个\n", stats.Clients))
	sb.WriteString(fmt.Sprintf("📶 流量：%.2f / %.0f GB 已用\n", stats.UsedGB, stats.QuotaGB))

	sb.WriteString("\n<b>最近 7 天</b>\n")
	for _, d := range stats.Daily {
		bar := ""
		if d.Revenue > 0 {
			n := int(d.Revenue/10) + 1
			if n > 10 {
				n = 10
			}
			bar = strings.Repeat("▮", n)
		}
		sb.WriteString(fmt.Sprintf("%s  ¥%-7.2f %s\n", d.Date, d.Revenue, bar))
	}

	if stats.Pending > 0 {
		sb.WriteString("\n👉 有待确认订单，发 /orders 处理。")
	}
	t.SendMsgToTgbot(chatId, sb.String())
}
