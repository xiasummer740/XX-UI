package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/XiaSummer740/XX-UI/database/model"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
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
		t.SendMsgToTgbot(chatId, "🛒 还没有配置套餐。")
		return
	}

	msg := "🛒 <b>流量套餐</b>\n\n请选择要购买的流量：\n"
	var rows [][]telego.InlineKeyboardButton
	for _, p := range plans {
		msg += fmt.Sprintf("\n• <b>%d GB</b>　%s", p.Gb, planPriceText(p))
		rows = append(rows, tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(fmt.Sprintf("%d GB", p.Gb)).WithCallbackData(fmt.Sprintf("shop_gb %d", p.Gb)),
		))
	}
	msg += "\n\n点下面的按钮继续。"
	t.SendMsgToTgbot(chatId, msg, tu.InlineKeyboard(rows...))
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
	t.SendMsgToTgbot(chatId, fmt.Sprintf("已选 <b>%d GB</b>，请选择付款周期：", gb), tu.InlineKeyboard(rows...))
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
	msg := fmt.Sprintf("🧾 <b>订单确认</b>\n\n套餐：<b>%d GB</b>\n周期：%s（%d 天）\n金额：<b>¥%s</b>\n",
		gb, cycleLabel(cycle), days, formatPrice(price))
	note := shop.GetShopPayNote()
	if note != "" {
		msg += "\n" + note + "\n"
	}
	msg += "\n付款后点下面的「✅ 我已付款」，我确认到账后会自动给你开通。"

	qr := shop.GetShopQrURL()
	keyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("✅ 我已付款").WithCallbackData(fmt.Sprintf("shop_paid %d %s", gb, cycle)),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("⬅️ 返回套餐").WithCallbackData("shop_back"),
		),
	)
	if qr != "" {
		// 先发收款码图片，再发确认文案
		t.sendShopQrPhoto(chatId, qr, msg)
		t.SendMsgToTgbot(chatId, "☝️ 上面是收款码，付款后点下面按钮。", keyboard)
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
		"📨 订单 #%d 已提交。\n\n%d GB / %s / ¥%s\n\n我确认收款后会自动开通，开通后订阅链接直接发给你。",
		order.Id, order.PlanGb, cycleLabel(order.Cycle), formatPrice(order.Price)))

	kb := tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("✅ 确认到账并开通").WithCallbackData(fmt.Sprintf("shop_ok %d", order.Id)),
			tu.InlineKeyboardButton("❌ 拒绝").WithCallbackData(fmt.Sprintf("shop_no %d", order.Id)),
		),
	)
	t.SendMsgToTgbotAdmins(fmt.Sprintf("🛒 <b>新订单</b>\n\n%s\n\n请核对收款后处理：", orderText(order)), kb)
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
	msg := fmt.Sprintf("✅ 订单 #%d 已开通\n\n%d GB / %s\n到期：%s 之后\n",
		order.Id, order.PlanGb, cycleLabel(order.Cycle), fmt.Sprintf("%d 天", order.Days))
	if err == nil && subURL != "" {
		msg += "\n<b>订阅链接</b>\n<code>" + subURL + "</code>"
		if subJsonURL != "" {
			msg += "\n\nJSON: <code>" + subJsonURL + "</code>"
		}
	} else {
		msg += "\n（订阅链接生成失败，请到面板里手动取）"
	}
	kb := tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("📶 我的流量").WithCallbackData("client_traffic"),
			tu.InlineKeyboardButton("🔗 单节点链接").WithCallbackData("client_individual_links"),
		),
	)
	t.SendMsgToTgbot(order.ChatId, msg, kb)
	t.SendMsgToTgbot(chatId, fmt.Sprintf("✅ 订单 #%d 已开通（客户端 %s）。", order.Id, order.ClientEmail))
}

// rejectShopOrder 管理员拒绝订单。
func (t *Tgbot) rejectShopOrder(chatId int64, adminId int64, orderId int) {
	shop := ShopService{}
	order, err := shop.RejectOrder(orderId, adminId, "管理员拒绝")
	if err != nil {
		t.SendMsgToTgbot(chatId, "❌ "+err.Error())
		return
	}
	t.SendMsgToTgbot(chatId, fmt.Sprintf("已拒绝订单 #%d。", order.Id))
	t.SendMsgToTgbot(order.ChatId, fmt.Sprintf(
		"❌ 订单 #%d 未通过。\n如有疑问请直接联系管理员（可能是没收到款或信息对不上）。", order.Id))
}

// sendShopOrders 管理员查看最近订单。
func (t *Tgbot) sendShopOrders(chatId int64, shop *ShopService) {
	orders, err := shop.ListOrders(20)
	if err != nil || len(orders) == 0 {
		t.SendMsgToTgbot(chatId, "还没有订单。")
		return
	}
	var sb strings.Builder
	sb.WriteString("📋 <b>最近订单</b>\n")
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

// shopOrder 供外部（面板）使用的构造器占位，避免未使用告警。
var _ = func() *model.Order { return nil }
