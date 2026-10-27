package service

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/XiaSummer740/XX-UI/database"
	"github.com/XiaSummer740/XX-UI/database/model"
	"github.com/XiaSummer740/XX-UI/logger"
	"github.com/XiaSummer740/XX-UI/xray"

	"github.com/op/go-logging"
)

var trafficResetLoggerOnce sync.Once

// setupTrafficResetDB wires a temporary sqlite database plus a log folder,
// because the reset paths emit warnings and the package logger must not be nil.
func setupTrafficResetDB(t *testing.T) {
	t.Helper()

	dbDir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbDir)

	trafficResetLoggerOnce.Do(func() {
		// The logger keeps the file open for the lifetime of the process, so it
		// must not live inside t.TempDir(): Windows would refuse to remove it.
		logDir, err := os.MkdirTemp("", "xxui-test-log")
		if err != nil {
			t.Fatalf("create log dir: %v", err)
		}
		t.Setenv("XUI_LOG_FOLDER", logDir)
		logger.InitLogger(logging.ERROR)
	})

	if err := database.InitDB(filepath.Join(dbDir, "XX-UI.db")); err != nil {
		t.Fatalf("init test db: %v", err)
	}
	// Registered after TempDir, so it runs first (LIFO) and Windows can
	// delete the database file.
	t.Cleanup(func() {
		if err := database.CloseDB(); err != nil {
			t.Logf("close db: %v", err)
		}
	})
}

func TestNormalizeTrafficReset(t *testing.T) {
	for _, method := range []string{"never", "hourly", "daily", "weekly", "monthly", "custom_date"} {
		if _, _, err := normalizeTrafficReset(method, 22); err != nil {
			t.Fatalf("%q should be accepted: %v", method, err)
		}
	}

	// A value that no cron job matches must be rejected instead of stored:
	// it would disable resets for that inbound while looking perfectly fine.
	for _, method := range []string{"", " ", "every_day", "Custom_Date", "custom-date", "0"} {
		if _, _, err := normalizeTrafficReset(method, 1); err == nil {
			t.Fatalf("%q should have been rejected", method)
		}
	}

	if m, d, err := normalizeTrafficReset("custom_date", 0); err != nil || m != "custom_date" || d != 1 {
		t.Fatalf("want custom_date/1, got %s/%d (err=%v)", m, d, err)
	}
	if m, d, err := normalizeTrafficReset(" custom_date ", 40); err != nil || m != "custom_date" || d != 1 {
		t.Fatalf("want trimmed custom_date/1, got %s/%d (err=%v)", m, d, err)
	}
}

func TestMergeTrafficResetPolicy(t *testing.T) {
	setupTrafficResetDB(t)

	old := &model.Inbound{TrafficReset: "custom_date", ResetDay: 22}

	// Fields missing from the request must keep the stored values.
	m, d, changed := mergeTrafficResetPolicy(&model.Inbound{}, old)
	if m != "custom_date" || d != 22 || changed {
		t.Fatalf("missing fields: want custom_date/22 unchanged, got %s/%d changed=%v", m, d, changed)
	}

	// Changing only the day must be detected.
	m, d, changed = mergeTrafficResetPolicy(&model.Inbound{ResetDay: 28}, old)
	if m != "custom_date" || d != 28 || !changed {
		t.Fatalf("day change: want custom_date/28 changed, got %s/%d changed=%v", m, d, changed)
	}

	// Switching to a plain period must be detected too.
	m, _, changed = mergeTrafficResetPolicy(&model.Inbound{TrafficReset: "never"}, old)
	if m != "never" || !changed {
		t.Fatalf("method change: want never changed, got %s changed=%v", m, changed)
	}

	// Garbage must fall back to the global default instead of failing an edit.
	m, _, _ = mergeTrafficResetPolicy(&model.Inbound{TrafficReset: "bogus"}, old)
	if m != "never" {
		t.Fatalf("invalid method: want fallback never, got %s", m)
	}
}

func TestApplyTrafficResetToAllRejectsBadMethod(t *testing.T) {
	setupTrafficResetDB(t)
	db := database.GetDB()

	inbound := &model.Inbound{
		Port: 21001, Protocol: model.HTTP, Settings: "{}", StreamSettings: "{}", Sniffing: "{}",
		TrafficReset: "custom_date", ResetDay: 22, LastTrafficResetTime: 1000,
	}
	if err := db.Create(inbound).Error; err != nil {
		t.Fatalf("create inbound: %v", err)
	}

	s := &InboundService{}
	if _, err := s.ApplyTrafficResetToAll("bogus", 5); err == nil {
		t.Fatal("invalid method must be rejected")
	}

	n, err := s.ApplyTrafficResetToAll("custom_date", 22)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 row updated, got %d", n)
	}

	var got model.Inbound
	if err := db.First(&got, inbound.Id).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.ResetDay != 22 || got.TrafficReset != "custom_date" {
		t.Fatalf("policy not applied: %s/%d", got.TrafficReset, got.ResetDay)
	}
	// Re-anchored so the catch-up logic cannot wipe the current period the
	// moment the administrator changes the policy.
	if got.LastTrafficResetTime <= 1000 {
		t.Fatalf("last reset time was not re-anchored: %d", got.LastTrafficResetTime)
	}
}

func TestResetInboundTrafficWithHistory(t *testing.T) {
	setupTrafficResetDB(t)
	db := database.GetDB()

	previousReset := time.Now().Add(-30 * 24 * time.Hour).UnixMilli()
	inbound := &model.Inbound{
		Port: 21002, Protocol: model.HTTP, Settings: "{}", StreamSettings: "{}", Sniffing: "{}",
		Enable: true, Up: 100, Down: 200,
		TrafficReset: "custom_date", ResetDay: 22, LastTrafficResetTime: previousReset,
	}
	if err := db.Create(inbound).Error; err != nil {
		t.Fatalf("create inbound: %v", err)
	}

	// A client the administrator disabled on purpose.
	client := &xray.ClientTraffic{
		InboundId: inbound.Id, Email: "reset-test@example.com",
		Enable: false, Up: 10, Down: 20,
	}
	if err := db.Create(client).Error; err != nil {
		t.Fatalf("create client traffic: %v", err)
	}

	s := &InboundService{}
	if err := s.ResetInboundTrafficWithHistory(inbound.Id, "custom_date"); err != nil {
		t.Fatalf("reset: %v", err)
	}

	var gotInbound model.Inbound
	if err := db.First(&gotInbound, inbound.Id).Error; err != nil {
		t.Fatalf("reload inbound: %v", err)
	}
	if gotInbound.Up != 0 || gotInbound.Down != 0 {
		t.Fatalf("inbound counters not cleared: up=%d down=%d", gotInbound.Up, gotInbound.Down)
	}
	if gotInbound.LastTrafficResetTime <= previousReset {
		t.Fatalf("last reset time not advanced: %d", gotInbound.LastTrafficResetTime)
	}

	var gotClient xray.ClientTraffic
	if err := db.First(&gotClient, client.Id).Error; err != nil {
		t.Fatalf("reload client: %v", err)
	}
	if gotClient.Up != 0 || gotClient.Down != 0 {
		t.Fatalf("client counters not cleared: up=%d down=%d", gotClient.Up, gotClient.Down)
	}
	if gotClient.Enable {
		t.Fatal("a periodic reset must not re-enable a client that was disabled on purpose")
	}

	var rows []model.TrafficHistory
	if err := db.Where("inbound_id = ?", inbound.Id).Find(&rows).Error; err != nil {
		t.Fatalf("load history: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 history rows (inbound total + client), got %d", len(rows))
	}

	total, found := int64(0), false
	for _, row := range rows {
		if row.Email == "" {
			found = true
			total = row.Up + row.Down
			if row.Up != 100 || row.Down != 200 {
				t.Fatalf("inbound history must record pre-reset values, got up=%d down=%d", row.Up, row.Down)
			}
			if row.PeriodStart != previousReset {
				t.Fatalf("history period start: want %d, got %d", previousReset, row.PeriodStart)
			}
		}
	}
	if !found || total != 300 {
		t.Fatalf("missing/invalid inbound total history row (found=%v total=%d)", found, total)
	}
}
