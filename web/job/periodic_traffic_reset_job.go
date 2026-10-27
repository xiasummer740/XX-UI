package job

import (
	"time"

	"github.com/XiaSummer740/XX-UI/database"
	"github.com/XiaSummer740/XX-UI/database/model"
	"github.com/XiaSummer740/XX-UI/logger"
	"github.com/XiaSummer740/XX-UI/web/service"

	"gorm.io/gorm"
)

// Period represents the time period for traffic resets.
type Period string

// PeriodicTrafficResetJob resets traffic statistics for inbounds based on their configured reset period.
type PeriodicTrafficResetJob struct {
	inboundService service.InboundService
	period         Period
}

// NewPeriodicTrafficResetJob creates a new periodic traffic reset job for the specified period.
func NewPeriodicTrafficResetJob(period Period) *PeriodicTrafficResetJob {
	return &PeriodicTrafficResetJob{
		period: period,
	}
}

// Run resets traffic statistics for all inbounds that match the configured reset period.
func (j *PeriodicTrafficResetJob) Run() {
	inbounds, err := j.getInboundsForPeriod()
	if err != nil {
		logger.Warning("Failed to get inbounds for traffic reset:", err)
		return
	}

	if len(inbounds) == 0 {
		return
	}
	logger.Infof("Running periodic traffic reset job for period: %s (%d matching inbounds)", j.period, len(inbounds))

	resetCount := 0
	skipped := 0

	for _, inbound := range inbounds {
		// 当期完全没有流量时不必凭空写一条历史记录，只推进重置时间，
		// 否则「漏跑补偿」会每天重复命中同一个入站。
		if inbound.Up+inbound.Down == 0 {
			if err := j.inboundService.MarkTrafficResetTime(inbound.Id); err != nil {
				logger.Warning("Failed to mark traffic reset time for inbound", inbound.Id, ":", err)
			}
			skipped++
			continue
		}

		// 先写快照再归零，否则「上期到底用了多少」就永远丢了
		if err := j.inboundService.ResetInboundTrafficWithHistory(inbound.Id, string(j.period)); err != nil {
			logger.Warning("Failed to reset traffic for inbound", inbound.Id, ":", err)
			continue
		}
		resetCount++
	}

	if skipped > 0 {
		logger.Infof("Skipped %d inbounds with no traffic in current period", skipped)
	}

	if resetCount > 0 {
		logger.Infof("Periodic traffic reset completed: %d inbounds reset", resetCount)
	}
}

// getInboundsForPeriod returns inbounds matching the configured period.
// For "custom_date", it additionally checks if today matches the inbound's resetDay.
func (j *PeriodicTrafficResetJob) getInboundsForPeriod() ([]*model.Inbound, error) {
	db := database.GetDB()

	if j.period == "custom_date" {
		// custom_date 每天跑一次：先处理「今天正好是重置日」，
		// 再做漏跑补偿——本月重置日已过但上次重置时间仍早于它，
		// 说明那天面板没在运行，这里补一次，避免整整一个月不清零。
		// 注意：不再限制 enable=true，已关闭的入站也应该归零，
		// 否则重新启用时拿到的还是旧数据。
		var allCustomInbounds []*model.Inbound
		err := db.Model(model.Inbound{}).
			Where("traffic_reset = ?", "custom_date").
			Find(&allCustomInbounds).Error
		if err != nil && err != gorm.ErrRecordNotFound {
			return nil, err
		}

		now := time.Now()
		today := now.Day()
		// 本月最后一天：重置日设为 29/30/31 时短月没有那一天，
		// 直接比较 today == 31 会导致该月永远不重置，故按「本月最后一天」兜底。
		lastDayOfMonth := time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, now.Location()).Day()

		var matching []*model.Inbound
		for _, inbound := range allCustomInbounds {
			resetDay := inbound.ResetDay
			if resetDay < 1 {
				resetDay = 1
			}
			if resetDay > 31 {
				resetDay = 31
			}
			if resetDay > lastDayOfMonth {
				resetDay = lastDayOfMonth
			}

			if today == resetDay {
				matching = append(matching, inbound)
				continue
			}

			// 漏跑补偿：本月重置日已过，且上次重置发生在它之前
			thisMonthReset := time.Date(now.Year(), now.Month(), resetDay, 0, 0, 0, 0, now.Location())
			if now.After(thisMonthReset) && inbound.LastTrafficResetTime < thisMonthReset.UnixMilli() {
				logger.Infof("Traffic reset missed for inbound %d (last: %d, due: %d), catching up",
					inbound.Id, inbound.LastTrafficResetTime, thisMonthReset.UnixMilli())
				matching = append(matching, inbound)
			}
		}
		return matching, nil
	}

	// Standard periods: query by exact match
	var inbounds []*model.Inbound
	err := db.Model(model.Inbound{}).Where("traffic_reset = ?", string(j.period)).Find(&inbounds).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, err
	}
	return inbounds, nil
}
