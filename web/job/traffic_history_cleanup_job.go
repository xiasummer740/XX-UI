package job

import (
	"time"

	"github.com/XiaSummer740/XX-UI/logger"
	"github.com/XiaSummer740/XX-UI/web/service"
)

// trafficHistoryRetention 是历史流量快照的保留时长。
// 快照每个重置周期每入站每客户端一行，不清理会无限增长；但这张表也是
// 「某月用了多少」的唯一来源，所以保留得足够长（一年）。
const trafficHistoryRetention = 365 * 24 * time.Hour

// TrafficHistoryCleanupJob 定期删除过期的流量历史快照。
type TrafficHistoryCleanupJob struct {
	inboundService service.InboundService
}

// NewTrafficHistoryCleanupJob creates a new cleanup job instance.
func NewTrafficHistoryCleanupJob() *TrafficHistoryCleanupJob {
	return &TrafficHistoryCleanupJob{}
}

// Run deletes history rows older than the retention window.
func (j *TrafficHistoryCleanupJob) Run() {
	cutoff := time.Now().Add(-trafficHistoryRetention).UnixMilli()
	deleted, err := j.inboundService.PruneTrafficHistory(cutoff)
	if err != nil {
		logger.Warning("Failed to prune traffic history:", err)
		return
	}
	if deleted > 0 {
		logger.Infof("Pruned %d traffic history rows older than %s", deleted, trafficHistoryRetention)
	}
}
