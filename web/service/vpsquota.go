package service

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/XiaSummer740/XX-UI/database"
	"github.com/XiaSummer740/XX-UI/database/model"
)

// VPS 套餐流量：面板只知道每个入站/客户端的用量，不知道整台 VPS 的套餐额度。
// 这里把「套餐总流量 + 重置日」做成可配置项，并据此算出当前计费周期的用量百分比，
// 到阈值时通过 Telegram 提醒。流量清零仍由现有的「流量重置」任务负责。

const (
	vpsQuotaNotifiedKey = "vpsQuotaNotified"
	// 默认提醒阈值：80% 和 95%
	defaultVPSQuotaNotify = "80,95"
)

// VPSUsageInbound 是单个入站在当前周期的用量，方便看谁在吃流量。
type VPSUsageInbound struct {
	Id     int    `json:"id"`
	Port   int    `json:"port"`
	Remark string `json:"remark"`
	Used   int64  `json:"used"`
}

// VPSUsage 描述整台 VPS 当前计费周期的流量使用情况。
type VPSUsage struct {
	QuotaBytes int64             `json:"quotaBytes"` // 套餐额度（字节），0 表示未配置
	QuotaGB    float64           `json:"quotaGB"`
	UsedBytes  int64             `json:"usedBytes"`
	Percent    float64           `json:"percent"`
	ResetDay   int               `json:"resetDay"`
	CycleStart int64             `json:"cycleStart"` // 秒
	CycleEnd   int64             `json:"cycleEnd"`   // 秒
	DaysLeft   int               `json:"daysLeft"`
	Thresholds []int             `json:"thresholds"`
	Notified   []int             `json:"notified"`
	Inbounds   []VPSUsageInbound `json:"inbounds"`
}

// ResetDayOf（1-31）在指定月份的日期，遇到小月自动收敛到当月最后一天。
func resetDayInMonth(year int, month time.Month, day int) time.Time {
	if day < 1 {
		day = 1
	}
	last := time.Date(year, month+1, 0, 0, 0, 0, 0, time.Local).Day()
	if day > last {
		day = last
	}
	return time.Date(year, month, day, 0, 0, 0, 0, time.Local)
}

// currentCycle 返回以 resetDay 为分界的当前计费周期（本地时区）。
func currentCycle(now time.Time, resetDay int) (start, end time.Time) {
	if resetDay < 1 || resetDay > 31 {
		resetDay = 1
	}
	thisMonth := resetDayInMonth(now.Year(), now.Month(), resetDay)
	if !now.Before(thisMonth) {
		start = thisMonth
		end = resetDayInMonth(now.Year(), now.Month()+1, resetDay)
	} else {
		start = resetDayInMonth(now.Year(), now.Month()-1, resetDay)
		end = thisMonth
	}
	return start, end
}

// parseThresholds 解析 "80,95" 这类阈值为升序整数列表。
func parseThresholds(s string) []int {
	out := make([]int, 0, 4)
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		v, err := strconv.Atoi(part)
		if err != nil || v <= 0 || v > 200 {
			continue
		}
		out = append(out, v)
	}
	sort.Ints(out)
	return out
}

// quotaNotifiedState 记录当前周期已经提醒过的阈值，避免重复轰炸。
type quotaNotifiedState struct {
	Cycle string `json:"cycle"`
	Sent  []int  `json:"sent"`
}

// GetVPSUsage 计算当前周期的整机流量用量。
func (s *InboundService) GetVPSUsage() (*VPSUsage, error) {
	var ss SettingService
	quotaGB, err := ss.GetVPSQuotaGB()
	if err != nil {
		quotaGB = 0
	}
	resetDay, err := ss.GetVPSResetDay()
	if err != nil || resetDay < 1 || resetDay > 31 {
		resetDay = 1
	}
	notifyCfg, err := ss.GetVPSQuotaNotify()
	if err != nil || strings.TrimSpace(notifyCfg) == "" {
		notifyCfg = defaultVPSQuotaNotify
	}

	var inbounds []*model.Inbound
	if err := database.GetDB().Model(model.Inbound{}).Find(&inbounds).Error; err != nil {
		return nil, err
	}

	now := time.Now()
	start, end := currentCycle(now, resetDay)

	usage := &VPSUsage{
		QuotaGB:    float64(quotaGB),
		QuotaBytes: int64(quotaGB) * 1024 * 1024 * 1024,
		ResetDay:   resetDay,
		CycleStart: start.Unix(),
		CycleEnd:   end.Unix(),
		DaysLeft:   int(end.Sub(now).Hours() / 24),
		Thresholds: parseThresholds(notifyCfg),
	}
	if usage.DaysLeft < 0 {
		usage.DaysLeft = 0
	}

	for _, ib := range inbounds {
		used := ib.Up + ib.Down
		usage.UsedBytes += used
		usage.Inbounds = append(usage.Inbounds, VPSUsageInbound{
			Id:     ib.Id,
			Port:   ib.Port,
			Remark: ib.Remark,
			Used:   used,
		})
	}
	sort.Slice(usage.Inbounds, func(i, j int) bool { return usage.Inbounds[i].Used > usage.Inbounds[j].Used })

	if usage.QuotaBytes > 0 {
		usage.Percent = float64(usage.UsedBytes) / float64(usage.QuotaBytes) * 100
	}
	usage.Notified = s.loadQuotaNotified(start)
	return usage, nil
}

// cycleKey 用作提醒状态的周期标识。
func cycleKey(start time.Time) string {
	return start.Format("2006-01-02")
}

func (s *InboundService) loadQuotaNotified(start time.Time) []int {
	var ss SettingService
	raw, err := ss.GetVPSQuotaNotified()
	if err != nil || raw == "" {
		return nil
	}
	var st quotaNotifiedState
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		return nil
	}
	if st.Cycle != cycleKey(start) {
		return nil
	}
	return st.Sent
}

// CheckVPSQuota 检查是否越过阈值，越过了就发 Telegram 提醒。
// 返回本次新提醒的阈值列表。同一周期内同一阈值只提醒一次。
func (s *InboundService) CheckVPSQuota(tgbot *Tgbot) ([]int, error) {
	var ss SettingService
	usage, err := s.GetVPSUsage()
	if err != nil {
		return nil, err
	}
	if usage.QuotaBytes <= 0 || len(usage.Thresholds) == 0 {
		return nil, nil
	}

	already := map[int]bool{}
	for _, v := range usage.Notified {
		already[v] = true
	}

	var newly []int
	for _, th := range usage.Thresholds {
		if usage.Percent >= float64(th) && !already[th] {
			newly = append(newly, th)
		}
	}
	if len(newly) == 0 {
		return nil, nil
	}

	start := time.Unix(usage.CycleStart, 0)
	sent := append(append([]int{}, usage.Notified...), newly...)
	sort.Ints(sent)
	if bs, err := json.Marshal(quotaNotifiedState{Cycle: cycleKey(start), Sent: sent}); err == nil {
		_ = ss.SetVPSQuotaNotified(string(bs))
	}

	if tgbot != nil {
		msg := fmt.Sprintf(
			"⚠️ VPS 流量提醒\n已用 %s / %s（%.1f%%）\n本周期：%s → %s\n剩余 %d 天",
			formatGB(usage.UsedBytes), formatGB(usage.QuotaBytes), usage.Percent,
			start.Format("2006-01-02"), time.Unix(usage.CycleEnd, 0).Format("2006-01-02"),
			usage.DaysLeft)
		tgbot.SendMsgToTgbotAdmins(msg)
	}
	return newly, nil
}

// formatGB 把字节数格式化成易读的大小。
func formatGB(b int64) string {
	const gb = 1024 * 1024 * 1024
	if b >= gb {
		return fmt.Sprintf("%.2f GB", float64(b)/gb)
	}
	return fmt.Sprintf("%.2f MB", float64(b)/1024/1024)
}
