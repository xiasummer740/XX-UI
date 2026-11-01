package service

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/XiaSummer740/XX-UI/database"
	"github.com/XiaSummer740/XX-UI/database/model"
	"github.com/XiaSummer740/XX-UI/logger"
)

// 提醒类型
const (
	shopNoticeUsage80  = "usage80"  // 流量用到 80%
	shopNoticeUsage100 = "usage100" // 流量用尽
	shopNoticeExpiring = "expiring" // 快到期（3 天内）
	shopNoticeExpired  = "expired"  // 已到期
)

// 每种提醒的冷却时间：同一件事在冷却期内只发一次，避免轰炸买家。
var shopNoticeCooldown = map[string]time.Duration{
	shopNoticeUsage80:  7 * 24 * time.Hour,
	shopNoticeUsage100: 24 * time.Hour,
	shopNoticeExpiring: 72 * time.Hour,
	shopNoticeExpired:  7 * 24 * time.Hour,
}

// NotifyClientUsageAndExpiry 检查商店入站里的客户端，用量/到期该提醒的推一条 TG。
// 这是提升续费率最直接的手段：快用完、快到期的时候提醒一句。
func (s *ShopService) NotifyClientUsageAndExpiry(bot *Tgbot) {
	if bot == nil || !s.GetUsageNotifyEnabled() {
		return
	}
	inboundId := s.GetShopInboundID()
	if inboundId <= 0 {
		return
	}
	inbound, err := s.inboundService.GetInbound(inboundId)
	if err != nil || inbound == nil {
		return
	}

	var settings struct {
		Clients []model.Client `json:"clients"`
	}
	if err := json.Unmarshal([]byte(inbound.Settings), &settings); err != nil {
		return
	}

	now := time.Now()
	for _, client := range settings.Clients {
		if client.Email == "" {
			continue
		}
		chatId := client.TgID
		if chatId == 0 {
			chatId = chatIdFromEmail(client.Email)
		}
		if chatId == 0 {
			continue // 不是商店卖的客户端，不管
		}

		used := int64(0)
		if tal, _, err := s.inboundService.GetClientByEmail(client.Email); err == nil && tal != nil {
			used = tal.Up + tal.Down
		}

		// 1) 到期相关
		if client.ExpiryTime > 0 {
			expireAt := time.UnixMilli(client.ExpiryTime)
			daysLeft := int(expireAt.Sub(now).Hours() / 24)
			switch {
			case expireAt.Before(now):
				s.sendShopNotice(bot, chatId, client.Email, shopNoticeExpired, func() string {
					return fmt.Sprintf("⌛ <b>流量已到期</b>\n\n你的 <b>%s</b> 已于 <b>%s</b> 到期，节点已经停了。\n\n"+
						"发 <b>/buy</b> 续一次，立刻恢复 ✅\n<i>续期是叠加的，提前续不影响你现在用。</i>",
						shopPlanLabel(client.TotalGB), expireAt.Format("01-02 15:04"))
				}())
			case daysLeft <= 3:
				s.sendShopNotice(bot, chatId, client.Email, shopNoticeExpiring, func() string {
					return fmt.Sprintf("⏰ <b>还有 %d 天到期</b>\n\n你的 <b>%s</b> 会在 <b>%s</b> 到期，到期后节点自动停用。\n\n"+
						"发 <b>/buy</b> 就能续，续的时间会往后叠加 💡",
						maxInt(daysLeft, 1), shopPlanLabel(client.TotalGB), expireAt.Format("01-02 15:04"))
				}())
			}
		}

		// 2) 流量相关
		if client.TotalGB > 0 {
			percent := int(used * 100 / client.TotalGB)
			switch {
			case used >= client.TotalGB:
				s.sendShopNotice(bot, chatId, client.Email, shopNoticeUsage100, func() string {
					return fmt.Sprintf("🛑 <b>流量用完了</b>\n\n你的 <b>%s</b> 已经用满 <b>%.0f GB</b>，节点已暂停。\n\n"+
						"发 <b>/buy</b> 续一次，马上恢复 ✅",
						shopPlanLabel(client.TotalGB), float64(used)/1024/1024/1024)
				}())
			case percent >= 80:
				s.sendShopNotice(bot, chatId, client.Email, shopNoticeUsage80, func() string {
					return fmt.Sprintf("📊 <b>流量快用完了</b>\n\n你的 <b>%s</b> 已经用了 <b>%d%%</b>（%.1f / %.0f GB）。\n\n"+
						"用完会被暂停，续费是<b>叠加</b>的，不会浪费 📈\n发 <b>/buy</b> 直接续。",
						shopPlanLabel(client.TotalGB), percent,
						float64(used)/1024/1024/1024, float64(client.TotalGB)/1024/1024/1024)
				}())
			}
		}
	}
}

// sendShopNotice 按冷却时间决定要不要真发，发过就记一笔。
func (s *ShopService) sendShopNotice(bot *Tgbot, chatId int64, email, kind string, msg string) {
	if msg == "" || chatId == 0 {
		return
	}
	cooldown, ok := shopNoticeCooldown[kind]
	if !ok {
		cooldown = 24 * time.Hour
	}
	db := database.GetDB()
	var count int64
	db.Model(&model.ShopNotice{}).
		Where("email = ? AND kind = ? AND sent_at > ?", email, kind, time.Now().Add(-cooldown).Unix()).
		Count(&count)
	if count > 0 {
		return
	}
	bot.SendMsgToTgbot(chatId, msg)
	if err := db.Create(&model.ShopNotice{Email: email, Kind: kind, SentAt: time.Now().Unix()}).Error; err != nil {
		logger.Debugf("[shop] 记录提醒失败 %s/%s: %v", email, kind, err)
	}
	logger.Info("[shop] 已提醒买家 ", chatId, "（", kind, "）")
}

// GetUsageNotifyEnabled 是否开启用量/到期提醒（默认开）。
func (s *ShopService) GetUsageNotifyEnabled() bool {
	v, err := s.settingService.getString("shopUsageNotify")
	if err != nil {
		return true
	}
	v = strings.TrimSpace(strings.ToLower(v))
	return v != "false" && v != "0" && v != "off"
}

// chatIdFromEmail 从 tg<用户ID> 这种 email 里还原 Telegram 用户 ID。
func chatIdFromEmail(email string) int64 {
	email = strings.TrimSpace(email)
	if !strings.HasPrefix(email, "tg") {
		return 0
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(email, "tg"), 10, 64)
	if err != nil {
		return 0
	}
	return id
}

// shopPlanLabel 把字节数说成人话。
func shopPlanLabel(totalBytes int64) string {
	if totalBytes <= 0 {
		return "套餐"
	}
	return fmt.Sprintf("%.0f GB 套餐", float64(totalBytes)/1024/1024/1024)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
