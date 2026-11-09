package job

import (
	"sync"
	"time"

	"github.com/XiaSummer740/XX-UI/logger"
	"github.com/XiaSummer740/XX-UI/web/service"
)

// SysmonJob 每 30 秒采一次服务器自身指标（CPU/内存/磁盘/网络/进程/连接数），保留 7 天。
type SysmonJob struct {
	sysmonService service.SysmonService
}

const sysmonInterval = 30 * time.Second

var (
	sysmonLastRun   time.Time
	sysmonLastRunMu sync.Mutex
)

// NewSysmonJob 创建采集任务。
func NewSysmonJob() *SysmonJob {
	return new(SysmonJob)
}

// Run 到点采一次；顺带每 10 轮清理一次过期数据。
func (j *SysmonJob) Run() {
	sysmonLastRunMu.Lock()
	due := sysmonLastRun.IsZero() || time.Since(sysmonLastRun) >= sysmonInterval
	if due {
		sysmonLastRun = time.Now()
	}
	sysmonLastRunMu.Unlock()
	if !due {
		return
	}
	if err := j.sysmonService.Collect(); err != nil {
		logger.Warning("采集服务器指标失败:", err)
		return
	}
	if time.Now().Minute()%10 == 0 {
		if n, err := j.sysmonService.Prune(service.SysRetentionDays); err == nil && n > 0 {
			logger.Debugf("清理服务器指标历史：删除 %d 行", n)
		}
	}
}
