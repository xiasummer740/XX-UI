package job

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/XiaSummer740/XX-UI/config"
	"github.com/XiaSummer740/XX-UI/database"
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

// Run executes the database backup into the configured backup directory.
//
// The snapshot is produced with SQLite's "VACUUM INTO", which reads through the
// normal connection under a proper transaction, so the copy is consistent even
// while the panel keeps writing (a plain file copy is not).
func (j *DbBackupJob) Run() {
	enabled, err := j.settingService.GetDbBackupEnabled()
	if err != nil || !enabled {
		return
	}

	backupPath, err := j.settingService.GetDbBackupPath()
	if err != nil || backupPath == "" {
		// Keep the default next to the database, whatever folder it lives in.
		backupPath = filepath.Join(config.GetDBFolderPath(), "backups")
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

	// Generate backup filename with timestamp
	timestamp := time.Now().Format("20060102_150405")
	backupFile := filepath.Join(backupPath, fmt.Sprintf("x-ui_%s.db", timestamp))

	if execErr := database.SnapshotTo(backupFile); execErr != nil {
		logger.Warning("Failed to create database backup:", execErr)
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
