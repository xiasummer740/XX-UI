package job

import (
	"github.com/XiaSummer740/XX-UI/web/service"
)

// ShopUsageJob 每 30 分钟检查一次商店客户端的用量/到期，该提醒的推 TG。
// 快用完、快到期的时候提醒一句，是最能提升续费的手段。
type ShopUsageJob struct {
	shopService  service.ShopService
	tgbotService service.Tgbot
}

// NewShopUsageJob 创建用量提醒任务。
func NewShopUsageJob() *ShopUsageJob {
	return new(ShopUsageJob)
}

// Run 执行一轮检查。
func (j *ShopUsageJob) Run() {
	j.shopService.NotifyClientUsageAndExpiry(j.tgbotService.NewTgbot())
}
