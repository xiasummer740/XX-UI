package job

import (
	"fmt"
	"testing"
	"time"

	"github.com/XiaSummer740/XX-UI/database"
	"github.com/XiaSummer740/XX-UI/database/model"
)

// TestCustomDateMatching covers the daily "custom_date" selection, including
// the catch-up path that used to clear freshly created inbounds.
func TestCustomDateMatching(t *testing.T) {
	setupIntegrationDB(t)
	db := database.GetDB()

	now := time.Now()
	today := now.Day()
	lastDayOfMonth := time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, now.Location()).Day()
	thisMonth := func(day int) int64 {
		return time.Date(now.Year(), now.Month(), day, 0, 0, 0, 0, now.Location()).UnixMilli()
	}

	port := 30000
	add := func(resetDay int, lastReset int64) int {
		port++
		inbound := &model.Inbound{
			Port:                 port,
			Tag:                  fmt.Sprintf("test-inbound-%d", port),
			Protocol:             model.HTTP,
			Settings:             "{}",
			StreamSettings:       "{}",
			Sniffing:             "{}",
			Enable:               true,
			TrafficReset:         "custom_date",
			ResetDay:             resetDay,
			LastTrafficResetTime: lastReset,
		}
		if err := db.Create(inbound).Error; err != nil {
			t.Fatalf("create inbound: %v", err)
		}
		return inbound.Id
	}

	idToday := add(today, thisMonth(today))

	var idNeverReset, idMissed, idFuture int
	if today > 1 {
		// Never reset (brand new / imported): must NOT be treated as a missed run.
		idNeverReset = add(1, 0)
		// Last reset happened before this month's reset day: catch up.
		idMissed = add(1, thisMonth(1)-10*24*3600*1000)
	}
	if today < lastDayOfMonth {
		// Reset day still ahead this month: must not match.
		idFuture = add(today+1, 0)
	}

	j := NewPeriodicTrafficResetJob("custom_date")
	matched, err := j.getInboundsForPeriod()
	if err != nil {
		t.Fatalf("getInboundsForPeriod: %v", err)
	}

	ids := make(map[int]bool, len(matched))
	for _, inbound := range matched {
		ids[inbound.Id] = true
	}

	if !ids[idToday] {
		t.Error("inbound whose reset day is today must be selected")
	}
	if idNeverReset != 0 && ids[idNeverReset] {
		t.Error("inbound with last_traffic_reset_time=0 must not be treated as a missed reset")
	}
	if idMissed != 0 && !ids[idMissed] {
		t.Error("inbound that missed this month's reset day must be caught up")
	}
	if idFuture != 0 && ids[idFuture] {
		t.Error("inbound whose reset day is still ahead must not be selected")
	}
}

// TestJobUsesBoundLocation makes sure day boundaries follow the panel time zone
// instead of the host one.
func TestJobUsesBoundLocation(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("time zone data unavailable: %v", err)
	}

	if got := NewPeriodicTrafficResetJob("custom_date").WithLocation(loc).now().Location(); got.String() != loc.String() {
		t.Fatalf("want %s, got %s", loc, got)
	}
	// Without WithLocation the job must stay on the host zone (and must not panic
	// on a nil location).
	if got := NewPeriodicTrafficResetJob("custom_date").now().Location(); got != time.Local {
		t.Fatalf("want local zone, got %s", got)
	}
	if got := NewPeriodicTrafficResetJob("custom_date").WithLocation(nil).now().Location(); got != time.Local {
		t.Fatalf("nil location must be ignored, got %s", got)
	}
}

// TestPeriodRunResetsAndKeepsDisabledClient documents the end-to-end behaviour
// of one job run: counters are zeroed, history is written, and a disabled
// client stays disabled.
func TestPeriodRunResetsAndKeepsDisabledClient(t *testing.T) {
	setupIntegrationDB(t)
	db := database.GetDB()

	now := time.Now()
	inbound := &model.Inbound{
		Tag: "test-inbound-31000", Port: 31000, Protocol: model.HTTP, Settings: "{}", StreamSettings: "{}", Sniffing: "{}",
		Enable: true, Up: 5, Down: 7,
		TrafficReset:         "custom_date",
		ResetDay:             now.Day(),
		LastTrafficResetTime: time.Now().Add(-40 * 24 * time.Hour).UnixMilli(),
	}
	if err := db.Create(inbound).Error; err != nil {
		t.Fatalf("create inbound: %v", err)
	}

	if err := db.Table("client_traffics").Create(map[string]any{
		"inbound_id":  inbound.Id,
		"enable":      false,
		"email":       "job-run@example.com",
		"up":          1,
		"down":        2,
		"expiry_time": 0,
		"total":       0,
		"reset":       0,
	}).Error; err != nil {
		t.Fatalf("create client traffic: %v", err)
	}

	NewPeriodicTrafficResetJob("custom_date").Run()

	var gotInbound model.Inbound
	if err := db.First(&gotInbound, inbound.Id).Error; err != nil {
		t.Fatalf("reload inbound: %v", err)
	}
	if gotInbound.Up != 0 || gotInbound.Down != 0 {
		t.Fatalf("counters not cleared: up=%d down=%d", gotInbound.Up, gotInbound.Down)
	}

	var enable bool
	row := db.Table("client_traffics").Select("enable").Where("email = ?", "job-run@example.com").Row()
	if err := row.Scan(&enable); err != nil {
		t.Fatalf("scan client enable: %v", err)
	}
	if enable {
		t.Fatal("periodic reset must not re-enable a client that was disabled")
	}

	var history int64
	db.Model(&model.TrafficHistory{}).Where("inbound_id = ?", inbound.Id).Count(&history)
	if history == 0 {
		t.Fatal("expected a traffic history snapshot")
	}
}
