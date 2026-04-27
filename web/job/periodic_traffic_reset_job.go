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

	for _, inbound := range inbounds {
		resetInboundErr := j.inboundService.ResetInboundTraffic(inbound.Id)
		if resetInboundErr != nil {
			logger.Warning("Failed to reset traffic for inbound", inbound.Id, ":", resetInboundErr)
		}

		resetClientErr := j.inboundService.ResetAllClientTraffics(inbound.Id)
		if resetClientErr != nil {
			logger.Warning("Failed to reset traffic for all users of inbound", inbound.Id, ":", resetClientErr)
		}

		if resetInboundErr == nil && resetClientErr == nil {
			resetCount++
		}
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
		// custom_date runs daily: find all inbounds with traffic_reset = "custom_date"
		// and check if today's day of month matches their resetDay
		var allCustomInbounds []*model.Inbound
		err := db.Model(model.Inbound{}).
			Where("traffic_reset = ?", "custom_date").
			Where("enable = ?", true).
			Find(&allCustomInbounds).Error
		if err != nil && err != gorm.ErrRecordNotFound {
			return nil, err
		}

		today := time.Now().Day()
		var matching []*model.Inbound
		for _, inbound := range allCustomInbounds {
			resetDay := inbound.ResetDay
			if resetDay < 1 {
				resetDay = 1
			}
			if resetDay > 31 {
				resetDay = 31
			}
			if today == resetDay {
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
