package service

import (
	"context"
	"encoding/base64"
	"fmt"
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
	case "paid":
		if len(args) < 3 {
			return true
		}
		gb, _ := strconv.Atoi(args[1])
		t.createShopOrder(chatId, &callbackQuery.From, gb, args[2])
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
	case "orders":
		if isAdmin {
			t.sendShopOrders(chatId, &shop)
		}
	}
	_ = shop
	return true
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

	msg := "🎁 <b>挑一个适合你的套餐</b>\n\n"
	for _, p := range plans {
		msg += fmt.Sprintf("▫️ <b>%d GB</b>  ·  %s\n", p.Gb, planPriceText(p))
	}
	msg += "\n点下面的按钮选流量，随时可以反悔 👇"

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
	t.SendMsgToTgbot(chatId, fmt.Sprintf("已选 <b>%d GB</b> ✨\n想按哪种方式付款？\n\n<i>季付、年付更划算，用得久就更省一点。</i>", gb), tu.InlineKeyboard(rows...))
}

// sendShopConfirm 展示订单确认页（金额 + 收款说明 + 我已付款）。
func (t *Tgbot) sendShopConfirm(chatId int64, gb int, cycle string) {
	shop := ShopService{}
	price, ok := shop.PriceOf(gb, cycle)
	if !ok {
		t.SendMsgToTgbot(chatId, "这个组合暂时不可购买。")
		return
	}
	days := cycleDays(cycle)
	msg := fmt.Sprintf("🧾 <b>订单确认</b>\n\n"+
		"套餐：<b>%d GB</b>\n"+
		"周期：%s（%d 天）\n"+
		"金额：<b>¥%s</b>\n",
		gb, cycleLabel(cycle), days, formatPrice(price))

	note := shop.GetShopPayNote()
	if note != "" {
		msg += "\n" + note
	}
	msg += "\n\n💛 付完记得回来点下面的「✅ 我已付款」，我看到就立刻给你开通。"

	qrWechat := shop.GetShopQrURL()
	qrAlipay := shop.GetShopQrURL2()
	keyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("✅ 我已付款").WithCallbackData(fmt.Sprintf("shop_paid %d %s", gb, cycle)),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("⬅️ 返回套餐").WithCallbackData("shop_back"),
		),
	)

	// 收款码：微信 / 支付宝 两个都发，买家任选
	sentQR := false
	if qrWechat != "" {
		t.sendShopQrPhoto(chatId, qrWechat, "💚 <b>微信支付</b> 扫这个")
		sentQR = true
	}
	if qrAlipay != "" {
		t.sendShopQrPhoto(chatId, qrAlipay, "💙 <b>支付宝</b> 扫这个")
		sentQR = true
	}
	if sentQR {
		t.SendMsgToTgbot(chatId, msg+"\n\n☝️ 上面两个收款码，哪个方便扫哪个。", keyboard)
		return
	}
	t.SendMsgToTgbot(chatId, msg, keyboard)
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
		ChatID:  tu.ID(chatId),
		Photo:   tu.FileFromBytes(png, "sub-qr.png"),
		Caption: caption,
	})
}

// createShopOrder 买家点「我已付款」：落单并通知管理员。
func (t *Tgbot) createShopOrder(chatId int64, from *telego.User, gb int, cycle string) {
	shop := ShopService{}
	name := from.FirstName
	if from.Username != "" {
		name += "(@" + from.Username + ")"
	}
	order, err := shop.CreateOrder(chatId, name, gb, cycle)
	if err != nil {
		t.SendMsgToTgbot(chatId, "❌ "+err.Error())
		return
	}

	t.SendMsgToTgbot(chatId, fmt.Sprintf(
		"💌 <b>收到啦，订单 #%d 已登记</b>\n\n"+
			"套餐：<b>%d GB</b>　%s\n"+
			"金额：<b>¥%s</b>\n\n"+
			"我核对到账后马上开通，订阅链接和二维码会直接发到这里，你不用守着刷 ⏳\n"+
			"<i>一般几分钟内就好，夜里可能慢一点点。</i>",
		order.Id, order.PlanGb, cycleLabel(order.Cycle), formatPrice(order.Price)))

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

	subURL, subJsonURL, err := t.buildSubscriptionURLs(order.ClientEmail)
	expireAt := time.Now().AddDate(0, 0, order.Days).Format("2006-01-02")
	msg := fmt.Sprintf("🎉 <b>开通好啦，欢迎上车！</b>\n\n"+
		"套餐：<b>%d GB</b>　%s\n"+
		"有效期到：<b>%s</b>\n",
		order.PlanGb, cycleLabel(order.Cycle), expireAt)
	if err == nil && subURL != "" {
		msg += "\n📎 <b>订阅链接</b>（长按或点一下即可复制）\n<code>" + subURL + "</code>"
		if subJsonURL != "" {
			msg += "\n\n🧩 JSON: <code>" + subJsonURL + "</code>"
		}
		msg += "\n\n⬇️ 下面还有一张二维码，扫码导入的是同一个订阅，二选一就行。\n" +
			"<i>用的时候有任何问题，直接回我一句就好 🌙</i>"
	} else {
		msg += "\n（订阅链接生成失败，请联系管理员手动获取）"
	}
	kb := tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("📶 我的流量").WithCallbackData("client_traffic"),
			tu.InlineKeyboardButton("📷 订阅二维码").WithCallbackData("client_qr_links"),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("🔗 单节点链接").WithCallbackData("client_individual_links"),
			tu.InlineKeyboardButton("🛒 再买一份").WithCallbackData("shop_back"),
		),
	)
	t.SendMsgToTgbot(order.ChatId, msg, kb)
	if err == nil && subURL != "" {
		t.sendContentQR(order.ChatId, subURL, "📷 用客户端扫这个二维码就是同一个订阅（链接和二维码二选一即可）")
	}
	t.SendMsgToTgbot(chatId, fmt.Sprintf("✅ 订单 #%d 已开通（客户端 %s，%d GB/%s），订阅已发给买家。",
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
