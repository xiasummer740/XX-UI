package job

import (
	"github.com/XiaSummer740/XX-UI/web/service"
)

// VPSQuotaJob 每小时检查一次整机流量是否越过提醒阈值。
// 套餐额度与重置日在面板「VPS 配置」里填，阈值默认 80% / 95%。
// 同一周期内同一阈值只会提醒一次（状态存在 settings.vpsQuotaNotified）。
type VPSQuotaJob struct {
	inboundService service.InboundService
	tgbotService   service.Tgbot
}

// NewVPSQuotaJob 创建任务实例。
func NewVPSQuotaJob() *VPSQuotaJob {
	return new(VPSQuotaJob)
}

// Run 执行一次流量阈值检查。
func (j *VPSQuotaJob) Run() {
	tgBot := j.tgbotService.NewTgbot()
	if _, err := j.inboundService.CheckVPSQuota(tgBot); err != nil {
		return
	}
}
