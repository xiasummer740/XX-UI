package job

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/XiaSummer740/XX-UI/logger"
	"github.com/XiaSummer740/XX-UI/web/service"
)

// DbBackupJob performs automatic database backups on a scheduled basis.
type DbBackupJob struct {
	settingService service.SettingService
}

// NewDbBackupJob creates a new database backup job.
func NewDbBackupJob() *DbBackupJob {
	return &DbBackupJob{}
}

// Run executes the database backup, copying the current database file
// to the configured backup directory with a timestamp filename.
func (j *DbBackupJob) Run() {
	enabled, err := j.settingService.GetDbBackupEnabled()
	if err != nil || !enabled {
		return
	}

	backupPath, err := j.settingService.GetDbBackupPath()
	if err != nil || backupPath == "" {
		backupPath = "/etc/x-ui/backups/"
	}

	retention, err := j.settingService.GetDbBackupRetention()
	if err != nil || retention <= 0 {
		retention = 30
	}

	// Ensure backup directory exists
	err = os.MkdirAll(backupPath, 0755)
	if err != nil {
		logger.Warning("Failed to create backup directory:", err)
		return
	}

	// Source database path
	dbPath := "/etc/x-ui/x-ui.db"

	// Check if source database exists
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		logger.Warning("Database file not found at", dbPath)
		return
	}

	// Generate backup filename with timestamp
	timestamp := time.Now().Format("20060102_150405")
	backupFile := filepath.Join(backupPath, fmt.Sprintf("x-ui_%s.db", timestamp))

	// Copy the database file
	sourceFile, err := os.Open(dbPath)
	if err != nil {
		logger.Warning("Failed to open database file:", err)
		return
	}
	defer sourceFile.Close()

	destFile, err := os.Create(backupFile)
	if err != nil {
		logger.Warning("Failed to create backup file:", err)
		return
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	if err != nil {
		logger.Warning("Failed to copy database file:", err)
		return
	}

	logger.Infof("Database backup created: %s", backupFile)

	// Cleanup old backups
	j.cleanupOldBackups(backupPath, retention)
}

// cleanupOldBackups removes backup files exceeding the retention count.
func (j *DbBackupJob) cleanupOldBackups(backupPath string, retention int) {
	files, err := os.ReadDir(backupPath)
	if err != nil {
		logger.Warning("Failed to read backup directory:", err)
		return
	}

	// Filter for backup files
	var backupFiles []string
	for _, f := range files {
		if !f.IsDir() && strings.HasPrefix(f.Name(), "x-ui_") && strings.HasSuffix(f.Name(), ".db") {
			backupFiles = append(backupFiles, f.Name())
		}
	}

	if len(backupFiles) <= retention {
		return
	}

	// Sort by name (timestamp) ascending
	sort.Strings(backupFiles)

	// Remove oldest files exceeding retention
	toRemove := len(backupFiles) - retention
	for i := 0; i < toRemove; i++ {
		filePath := filepath.Join(backupPath, backupFiles[i])
		err := os.Remove(filePath)
		if err != nil {
			logger.Warning("Failed to remove old backup:", filePath, err)
		}
	}

	logger.Infof("Cleaned up %d old backup(s), keeping %d most recent", toRemove, retention)
}
