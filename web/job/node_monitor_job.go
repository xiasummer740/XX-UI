package job

import (
	"github.com/XiaSummer740/XX-UI/web/service"
)

// NodeMonitorJob 定时探测每个启用入站，连续失败/恢复时发 Telegram。
type NodeMonitorJob struct {
	inboundService service.InboundService
	tgbotService   service.Tgbot
}

// NewNodeMonitorJob 创建监控任务。
func NewNodeMonitorJob() *NodeMonitorJob {
	return new(NodeMonitorJob)
}

// Run 执行一轮探测。
func (j *NodeMonitorJob) Run() {
	tgBot := j.tgbotService.NewTgbot()
	j.inboundService.RunNodeMonitor(tgBot)
	// 顺带做一轮「真实可用」实测（10 分钟节流，单节点单次）
	j.inboundService.RunNodeProbes()
}
