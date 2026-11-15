package job

import (
	"fmt"
	"time"

	"github.com/XiaSummer740/XX-UI/database"
	"github.com/XiaSummer740/XX-UI/database/model"
	"github.com/XiaSummer740/XX-UI/logger"
	"github.com/XiaSummer740/XX-UI/web/service"
)

// ShopDigestJob 每 10 分钟把「近 24 小时订单对账结果」推给管理员：
// 目的是让你不用盯面板也知道有没有单卡住、有没有到账没开通。
type ShopDigestJob struct{}

func NewShopDigestJob() *ShopDigestJob { return &ShopDigestJob{} }

func (j *ShopDigestJob) Run() {
	shop := service.ShopService{}
	if ok, _ := shop.GetShopEnable(); !ok {
		return
	}
	var total, approved, pending, review, expired int64
	since := time.Now().Add(-24 * time.Hour).Unix()
	db := database.GetDB().Model(&model.Order{}).Where("created_at >= ?", since)
	db.Count(&total)
	database.GetDB().Model(&model.Order{}).Where("created_at >= ? AND status = ?", since, "approved").Count(&approved)
	database.GetDB().Model(&model.Order{}).Where("created_at >= ? AND status = ?", since, "pending").Count(&pending)
	database.GetDB().Model(&model.Order{}).Where("created_at >= ? AND status = ?", since, "review").Count(&review)
	database.GetDB().Model(&model.Order{}).Where("created_at >= ? AND status = ?", since, "expired").Count(&expired)

	// 只在有「需要人处理」的单时才打扰你
	if review == 0 && expired == 0 {
		logger.Infof("[shop] 对账日报：24h 内 %d 单（已开通 %d / 待付 %d），无需处理", total, approved, pending)
		return
	}
	msg := fmt.Sprintf("📊 <b>订单对账（近 24 小时）</b>\n\n总单：%d\n已开通：%d\n待付款：%d\n待核对：%d\n已过期：%d\n\n", total, approved, pending, review, expired)
	if review > 0 {
		msg += "⚠️ 有「待核对」订单，可能是买家付了但没自动识别，去面板点确认到账即可。\n"
	}
	if expired > 0 {
		msg += "🕒 有已过期订单，如买家说付了就手动确认。\n"
	}
	var bot service.Tgbot
	bot.SendMsgToTgbotAdmins(msg)
	logger.Infof("[shop] 对账日报已推送：review=%d expired=%d", review, expired)
}
