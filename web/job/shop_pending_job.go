package job

import (
	"github.com/XiaSummer740/XX-UI/web/service"
)

// ShopPendingJob 每 5 分钟检查一次「买家点了我已付款、但管理员还没确认」的订单，
// 等太久就再提醒一次管理员，避免漏单。
type ShopPendingJob struct {
	shopService  service.ShopService
	tgbotService service.Tgbot
}

// NewShopPendingJob 创建流量商店待确认订单提醒任务。
func NewShopPendingJob() *ShopPendingJob {
	return new(ShopPendingJob)
}

// Run 执行一轮提醒。
func (j *ShopPendingJob) Run() {
	j.shopService.RemindPendingOrders(j.tgbotService.NewTgbot())
}
