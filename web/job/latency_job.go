package job

import (
	"sync"
	"time"

	"github.com/XiaSummer740/XX-UI/logger"
	"github.com/XiaSummer740/XX-UI/web/service"
)

// LatencyMonitorJob 定时探测国内三网延迟 / 丢包，写入 30 天历史。
//
// cron 本身每分钟触发一次（便宜），真正的探测频率由设置里的
// latencyInterval（分钟）决定；这样改配置不用重新注册 cron。
type LatencyMonitorJob struct {
	latencyService service.LatencyService
	settingService service.SettingService
}

var (
	latencyLastRun   time.Time
	latencyLastRunMu sync.Mutex
	// 降采样节流：半小时一次就够
	latencyLastRollup time.Time
	latencyRollupMu   sync.Mutex
)

// NewLatencyMonitorJob 创建监控任务。
func NewLatencyMonitorJob() *LatencyMonitorJob {
	return new(LatencyMonitorJob)
}

// Run 到点就跑一轮；探测本身并发执行，单轮通常 2-6 秒。
func (j *LatencyMonitorJob) Run() {
	enabled, interval, retention, _ := j.settingService.GetLatencyConfig()
	if !enabled {
		return
	}
	if interval <= 0 {
		interval = 300
	}

	latencyLastRunMu.Lock()
	// interval 单位是秒（最小 1 秒）；单轮探测本身要 1-6 秒，
	// 间隔比这个小时会自动背靠背跑，不会堆任务。
	due := latencyLastRun.IsZero() || time.Since(latencyLastRun) >= time.Duration(interval)*time.Second
	if due {
		latencyLastRun = time.Now()
	}
	latencyLastRunMu.Unlock()

	if !due {
		return
	}

	n, err := j.latencyService.RunOnce()
	if err != nil {
		logger.Warning("三网延迟探测失败:", err)
		return
	}
	if n > 0 {
		logger.Debugf("三网延迟探测完成：%d 个测点", n)
	}

	// 每轮顺带裁剪过期数据（保留期内不算老）
	deleted, err := j.latencyService.Prune(retention)
	if err != nil {
		logger.Warning("清理三网延迟历史失败:", err)
		return
	}
	if deleted > 0 {
		logger.Infof("清理三网延迟历史：删除 %d 行（保留 %d 天）", deleted, retention)
	}
	// 每 30 分钟把 2 天前的原始点（5 秒一个）压成 1 分钟一个，
	// 否则 30 天历史早就顶到行数上限，7d/30d 只能看到最近两三天。
	latencyRollupMu.Lock()
	rollupDue := latencyLastRollup.IsZero() || time.Since(latencyLastRollup) >= 30*time.Minute
	if rollupDue {
		latencyLastRollup = time.Now()
	}
	latencyRollupMu.Unlock()
	if rollupDue {
		if del, add, err := j.latencyService.RollupOlderThan(2); err != nil {
			logger.Warning("三网延迟历史降采样失败:", err)
		} else if del > 0 {
			logger.Infof("三网延迟历史降采样：%d 行合并为 %d 行", del, add)
		}
	}

	// 间隔调到 1 秒时行数会暴涨，超过上限就丢最老的，保证库不会无限长
	if trimmed, err := j.latencyService.TrimToMaxRows(service.LatencyMaxRows); err != nil {
		logger.Warning("三网延迟数据裁剪失败:", err)
	} else if trimmed > 0 {
		logger.Infof("三网延迟数据超过上限，删除最老的 %d 行", trimmed)
	}
}

// RunNow 供「立即测一次」按钮使用，不受间隔限制。
func (j *LatencyMonitorJob) RunNow() (int, error) {
	n, err := j.latencyService.RunOnce()
	if err != nil {
		return 0, err
	}
	latencyLastRunMu.Lock()
	latencyLastRun = time.Now()
	latencyLastRunMu.Unlock()
	return n, nil
}
