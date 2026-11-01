package job

import (
	"github.com/XiaSummer740/XX-UI/logger"
	"github.com/XiaSummer740/XX-UI/web/service"
)

// DbEmailBackupJob 每天把数据库备份发到指定邮箱，
// 避免机器整台挂掉时数据跟着一起消失。
type DbEmailBackupJob struct {
	mailBackupService service.MailBackupService
	tgbotService      service.Tgbot
}

// NewDbEmailBackupJob 创建邮箱备份任务。
func NewDbEmailBackupJob() *DbEmailBackupJob {
	return new(DbEmailBackupJob)
}

// Run 执行一次备份并发送。
func (j *DbEmailBackupJob) Run() {
	if !j.mailBackupService.IsEnabled() {
		return
	}
	msg, err := j.mailBackupService.SendBackupByEmail()
	if err != nil {
		logger.Warning("[backup] 每日邮箱备份失败: ", err)
		// 备份发不出去必须让你知道，否则等于没备份
		bot := j.tgbotService.NewTgbot()
		bot.SendMsgToTgbotAdmins("⚠️ <b>每日邮箱备份失败</b>\n\n原因：" + err.Error() +
			"\n\n去 面板 → 设置 → 数据库备份 里看一眼 SMTP 配置，点「测试发送」可以马上验证。")
		return
	}
	logger.Info("[backup] 每日邮箱备份完成: ", msg)
}
