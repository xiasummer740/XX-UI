package job

import (
	"fmt"
	"strings"

	"github.com/XiaSummer740/XX-UI/banqueue"
	"github.com/XiaSummer740/XX-UI/logger"
	"github.com/XiaSummer740/XX-UI/web/service"
)

// HealthCheckJob 每天自动跑一遍入站体检，并把问题推送到 Telegram。
type HealthCheckJob struct {
	inboundService service.InboundService
	tgbotService   service.Tgbot
}

func NewHealthCheckJob() *HealthCheckJob {
	return new(HealthCheckJob)
}

func (j *HealthCheckJob) Run() {
	defer func() {
		if r := recover(); r != nil {
			logger.Warning("自动体检任务异常:", r)
		}
	}()
	j.inboundService.RunScheduledHealthCheck(j.tgbotService.NewTgbot())
}

// SubGuardNotifyJob 把订阅守卫的拉黑事件汇总后推送到 Telegram。
type SubGuardNotifyJob struct {
	settingService service.SettingService
	tgbotService   service.Tgbot
}

func NewSubGuardNotifyJob() *SubGuardNotifyJob {
	return new(SubGuardNotifyJob)
}

func (j *SubGuardNotifyJob) Run() {
	defer func() {
		if r := recover(); r != nil {
			logger.Warning("订阅守卫通知任务异常:", r)
		}
	}()
	events := banqueue.Drain()
	if len(events) == 0 {
		return
	}
	enable, err := j.settingService.GetSubGuardEnable()
	if err != nil || !enable {
		return
	}

	// 同一个 IP 只报一次，避免刷屏
	seen := map[string]int{}
	reasons := map[string]string{}
	order := []string{}
	for _, e := range events {
		if _, ok := seen[e.IP]; !ok {
			order = append(order, e.IP)
			reasons[e.IP] = e.Reason
		}
		seen[e.IP]++
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🚫 订阅防护拦下了 %d 个 IP\n", len(order)))
	limit := len(order)
	if limit > 10 {
		limit = 10
	}
	for _, ip := range order[:limit] {
		sb.WriteString(fmt.Sprintf("\n· %s ×%d\n  %s\n", ip, seen[ip], reasons[ip]))
	}
	if len(order) > limit {
		sb.WriteString(fmt.Sprintf("\n…另有 %d 个 IP 未列出", len(order)-limit))
	}
	sb.WriteString("\n\n提示：若这是你自己的设备，检查客户端的 User-Agent 或刷新频率。")
	j.tgbotService.NewTgbot().SendMsgToTgbotAdmins(sb.String())
}
