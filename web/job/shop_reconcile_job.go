package job

import (
	"github.com/XiaSummer740/XX-UI/web/service"
)

// ShopReconcileJob 每 20 秒主动向 BufPay 查一次待确认订单。
// BufPay 的回调经常送不到（非标准端口、签名规则变动都遇到过），
// 靠查单兜底以后，钱到了就一定会开通。
type ShopReconcileJob struct {
	shopService  service.ShopService
	tgbotService service.Tgbot
}

// NewShopReconcileJob 创建主动查单任务。
func NewShopReconcileJob() *ShopReconcileJob {
	return new(ShopReconcileJob)
}

// Run 执行一轮查单。
func (j *ShopReconcileJob) Run() {
	j.shopService.ReconcilePendingOrders(j.tgbotService.NewTgbot())
}
