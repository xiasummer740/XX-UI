package xray

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/XiaSummer740/XX-UI/config"
	"github.com/XiaSummer740/XX-UI/logger"
	"github.com/XiaSummer740/XX-UI/util/common"
)

// GetBinaryName returns the Xray binary filename for the current OS and architecture.
func GetBinaryName() string {
	return fmt.Sprintf("xray-%s-%s", runtime.GOOS, runtime.GOARCH)
}

// GetBinaryPath returns the full path to the Xray binary executable.
func GetBinaryPath() string {
	return config.GetBinFolderPath() + "/" + GetBinaryName()
}

// GetConfigPath returns the path to the Xray configuration file in the binary folder.
func GetConfigPath() string {
	return config.GetBinFolderPath() + "/config.json"
}

// GetGeositePath returns the path to the geosite data file used by Xray.
func GetGeositePath() string {
	return config.GetBinFolderPath() + "/geosite.dat"
}

// GetGeoipPath returns the path to the geoip data file used by Xray.
func GetGeoipPath() string {
	return config.GetBinFolderPath() + "/geoip.dat"
}

// GetIPLimitLogPath returns the path to the IP limit log file.
func GetIPLimitLogPath() string {
	return config.GetLogFolder() + "/3xipl.log"
}

// GetIPLimitBannedLogPath returns the path to the banned IP log file.
func GetIPLimitBannedLogPath() string {
	return config.GetLogFolder() + "/3xipl-banned.log"
}

// GetIPLimitBannedPrevLogPath returns the path to the previous banned IP log file.
func GetIPLimitBannedPrevLogPath() string {
	return config.GetLogFolder() + "/3xipl-banned.prev.log"
}

// GetAccessPersistentLogPath returns the path to the persistent access log file.
func GetAccessPersistentLogPath() string {
	return config.GetLogFolder() + "/3xipl-ap.log"
}

// GetAccessPersistentPrevLogPath returns the path to the previous persistent access log file.
func GetAccessPersistentPrevLogPath() string {
	return config.GetLogFolder() + "/3xipl-ap.prev.log"
}

// GetAccessLogPath reads the Xray config and returns the access log file path.
func GetAccessLogPath() (string, error) {
	config, err := os.ReadFile(GetConfigPath())
	if err != nil {
		logger.Warningf("Failed to read configuration file: %s", err)
		return "", err
	}

	jsonConfig := map[string]any{}
	err = json.Unmarshal([]byte(config), &jsonConfig)
	if err != nil {
		logger.Warningf("Failed to parse JSON configuration: %s", err)
		return "", err
	}

	if jsonConfig["log"] != nil {
		jsonLog, ok := jsonConfig["log"].(map[string]any)
		if ok && jsonLog["access"] != nil {
			accessLogPath, ok := jsonLog["access"].(string)
			if ok {
				return accessLogPath, nil
			}
		}
	}
	return "", nil
}

// stopProcess calls Stop on the given Process instance.
func stopProcess(p *Process) {
	p.Stop()
}

// Process wraps an Xray process instance and provides management methods.
type Process struct {
	*process
}

// NewProcess creates a new Xray process and sets up cleanup on garbage collection.
func NewProcess(xrayConfig *Config) *Process {
	p := &Process{newProcess(xrayConfig)}
	runtime.SetFinalizer(p, stopProcess)
	return p
}

// NewTestProcess creates a new Xray process that uses a specific config file path.
// Used for test runs (e.g. outbound test) so the main config.json is not overwritten.
// The config file at configPath is removed when the process is stopped.
func NewTestProcess(xrayConfig *Config, configPath string) *Process {
	p := &Process{newTestProcess(xrayConfig, configPath)}
	runtime.SetFinalizer(p, stopProcess)
	return p
}

type process struct {
	cmd *exec.Cmd

	version string
	apiPort int

	onlineClients []string

	config     *Config
	configPath string // if set, use this path instead of GetConfigPath() and remove on Stop
	logWriter  *LogWriter
	exitErr    error
	startTime  time.Time
}

// newProcess creates a new internal process struct for Xray.
func newProcess(config *Config) *process {
	return &process{
		version:   "Unknown",
		config:    config,
		logWriter: NewLogWriter(),
		startTime: time.Now(),
	}
}

// newTestProcess creates a process that writes and runs with a specific config path.
func newTestProcess(config *Config, configPath string) *process {
	p := newProcess(config)
	p.configPath = configPath
	return p
}

// IsRunning returns true if the Xray process is currently running.
func (p *process) IsRunning() bool {
	if p.cmd == nil || p.cmd.Process == nil {
		return false
	}
	if p.cmd.ProcessState == nil {
		return true
	}
	return false
}

// GetErr returns the last error encountered by the Xray process.
func (p *process) GetErr() error {
	return p.exitErr
}

// GetResult returns the last log line or error from the Xray process.
func (p *process) GetResult() string {
	if len(p.logWriter.lastLine) == 0 && p.exitErr != nil {
		return p.exitErr.Error()
	}
	return p.logWriter.lastLine
}

// GetVersion returns the version string of the Xray process.
func (p *process) GetVersion() string {
	return p.version
}

// GetAPIPort returns the API port used by the Xray process.
func (p *Process) GetAPIPort() int {
	return p.apiPort
}

// GetConfig returns the configuration used by the Xray process.
func (p *Process) GetConfig() *Config {
	return p.config
}

// GetOnlineClients returns the list of online clients for the Xray process.
func (p *Process) GetOnlineClients() []string {
	return p.onlineClients
}

// SetOnlineClients sets the list of online clients for the Xray process.
func (p *Process) SetOnlineClients(users []string) {
	p.onlineClients = users
}

// GetUptime returns the uptime of the Xray process in seconds.
func (p *Process) GetUptime() uint64 {
	return uint64(time.Since(p.startTime).Seconds())
}

// refreshAPIPort updates the API port from the inbound configs.
func (p *process) refreshAPIPort() {
	for _, inbound := range p.config.InboundConfigs {
		if inbound.Tag == "api" {
			p.apiPort = inbound.Port
			break
		}
	}
}

// refreshVersion updates the version string by running the Xray binary with -version.
func (p *process) refreshVersion() {
	cmd := exec.Command(GetBinaryPath(), "-version")
	data, err := cmd.Output()
	if err != nil {
		p.version = "Unknown"
	} else {
		datas := bytes.Split(data, []byte(" "))
		if len(datas) <= 1 {
			p.version = "Unknown"
		} else {
			p.version = string(datas[1])
		}
	}
}

// KillOrphanXray kills any orphaned xray processes that might be running
// from a previous panel instance. On Unix, it uses pkill to find and terminate
// processes matching the xray binary name, excluding the current process.
func KillOrphanXray() {
	if runtime.GOOS == "windows" {
		return
	}

	binaryName := GetBinaryName()

	// Use pgrep to find xray process IDs
	pgrep := exec.Command("pgrep", "-x", binaryName)
	output, err := pgrep.Output()
	if err != nil {
		// pgrep exits with code 1 if no processes found - not an error
		return
	}

	currentPID := os.Getpid()
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, line := range lines {
		pidStr := strings.TrimSpace(line)
		if pidStr == "" {
			continue
		}
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			continue
		}
		if pid == currentPID {
			continue
		}

		// Verify it's an orphan (parent PID is 1)
		ppidBytes, err := os.ReadFile(filepath.Join("/proc", pidStr, "status"))
		if err != nil {
			// Process might have exited already
			continue
		}
		isOrphan := false
		for _, line := range strings.Split(string(ppidBytes), "\n") {
			if strings.HasPrefix(line, "PPid:") {
				ppid := strings.TrimSpace(strings.TrimPrefix(line, "PPid:"))
				if ppid == "1" {
					isOrphan = true
				}
				break
			}
		}

		if isOrphan {
			logger.Warningf("Found orphaned xray process (PID %d), terminating...", pid)
			proc, err := os.FindProcess(pid)
			if err == nil {
				_ = proc.Signal(syscall.SIGTERM)
				// Wait briefly for graceful shutdown
				time.Sleep(500 * time.Millisecond)
				// Force kill if still alive
				_ = proc.Signal(syscall.SIGKILL)
			}
		}
	}
}

// geoDataURLs provides the download URLs for geoip.dat and geosite.dat files.
var geoDataURLs = map[string]string{
	"geoip.dat":   "https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geoip.dat",
	"geosite.dat": "https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geosite.dat",
}

// ensureGeoDataFiles checks if geoip.dat and geosite.dat exist in the binary folder.
// If any are missing, they are downloaded automatically from GitHub to prevent xray
// from failing to start due to missing route data files (e.g., geoip:private rule).
func ensureGeoDataFiles() {
	binFolder := config.GetBinFolderPath()

	// Ensure bin directory exists
	if err := os.MkdirAll(binFolder, 0o755); err != nil {
		logger.Warningf("Failed to create bin folder for geo data: %s", err)
		return
	}

	client := &http.Client{Timeout: 30 * time.Second}

	for filename, url := range geoDataURLs {
		filePath := filepath.Join(binFolder, filename)

		// Check if file already exists
		if _, err := os.Stat(filePath); err == nil {
			continue // File exists, skip download
		}

		logger.Infof("Geo data file %s not found at %s, downloading from %s ...", filename, filePath, url)

		resp, err := client.Get(url)
		if err != nil {
			logger.Warningf("Failed to download %s: %v", filename, err)
			continue
		}

		out, err := os.Create(filePath)
		if err != nil {
			logger.Warningf("Failed to create %s: %v", filePath, err)
			resp.Body.Close()
			continue
		}

		written, err := io.Copy(out, resp.Body)
		resp.Body.Close()
		out.Close()

		if err != nil {
			logger.Warningf("Failed to write %s: %v", filename, err)
			os.Remove(filePath)
			continue
		}

		logger.Infof("Successfully downloaded %s (%d bytes)", filename, written)
	}
}

// Start launches the Xray process with the current configuration.
// Returns an error immediately if the binary is missing, the config is invalid,
// or the process exits within the startup grace period (1 second).
func (p *process) Start() (err error) {
	if p.IsRunning() {
		return errors.New("xray is already running")
	}

	// Kill any orphaned xray processes from previous panel instances
	KillOrphanXray()

	defer func() {
		if err != nil {
			logger.Error("Failure in running xray-core process: ", err)
			p.exitErr = err
		}
	}()

	data, err := json.MarshalIndent(p.config, "", "  ")
	if err != nil {
		return common.NewErrorf("Failed to generate XRAY configuration files: %v", err)
	}

	err = os.MkdirAll(config.GetLogFolder(), 0o770)
	if err != nil {
		logger.Warningf("Failed to create log folder: %s", err)
	}

	configPath := GetConfigPath()
	if p.configPath != "" {
		configPath = p.configPath
	}

	// Ensure the bin directory exists before writing config.json.
	// Without this, the write will fail with "no such file or directory"
	// when the panel is run from a directory that doesn't have a bin/ folder.
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return common.NewErrorf("Failed to create directory for config file: %v", err)
	}

	logger.Infof("Writing xray config to: %s", configPath)
	err = os.WriteFile(configPath, data, fs.ModePerm)
	if err != nil {
		return common.NewErrorf("Failed to write configuration file: %v", err)
	}

	binaryPath := GetBinaryPath()
	logger.Infof("Starting xray binary: %s", binaryPath)

	// Check if binary exists before attempting to start
	if _, err := os.Stat(binaryPath); os.IsNotExist(err) {
		// Try to find xray binary from common alternative locations
		// (e.g., previous installation at /usr/local/x-ui/bin/)
		if found := tryFindXrayBinary(binaryPath); found {
			logger.Infof("Found and copied xray binary to: %s", binaryPath)
		} else {
			return common.NewErrorf("Xray binary not found at: %s", binaryPath)
		}
	}

	// Ensure geoip.dat and geosite.dat are present before starting xray.
	// These files are required by routing rules like "geoip:private" and
	// will be downloaded automatically from GitHub if missing.
	ensureGeoDataFiles()

	cmd := exec.Command(binaryPath, "-c", configPath)
	p.cmd = cmd

	cmd.Stdout = p.logWriter
	cmd.Stderr = p.logWriter

	// Use Start() instead of Run() so we can monitor the process startup
	err = cmd.Start()
	if err != nil {
		return common.NewErrorf("Failed to start xray process: %v", err)
	}

	// Channel to signal process exit from the monitoring goroutine
	processExited := make(chan error, 1)

	go func() {
		waitErr := cmd.Wait()
		if waitErr != nil {
			// On Windows, killing the process results in "exit status 1" which isn't an error for us
			if runtime.GOOS == "windows" {
				errStr := strings.ToLower(waitErr.Error())
				if strings.Contains(errStr, "exit status 1") {
					// Suppress noisy log on graceful stop
					p.exitErr = waitErr
					processExited <- waitErr
					return
				}
			}
			logger.Error("Failure in running xray-core:", waitErr)
			p.exitErr = waitErr
		}
		processExited <- waitErr
	}()

	// Wait up to 1 second for the process to either stabilize or crash.
	// This ensures that startup failures (e.g., port conflict, invalid config,
	// missing binary) are captured and returned to the caller instead of being
	// silently swallowed in a background goroutine.
	select {
	case waitErr := <-processExited:
		// Process exited within the startup grace period — treat as startup failure
		errMsg := "xray process exited immediately after start"
		if waitErr != nil {
			return common.NewErrorf("%s: %v", errMsg, waitErr)
		}
		return common.NewError(errMsg)
	case <-time.After(1 * time.Second):
		// Process is still running after the grace period — startup succeeded
		p.refreshVersion()
		p.refreshAPIPort()
		return nil
	}
}

// Stop terminates the running Xray process.
func (p *process) Stop() error {
	if !p.IsRunning() {
		return errors.New("xray is not running")
	}

	// Remove temporary config file used for test runs so main config is never touched
	if p.configPath != "" {
		if p.configPath != GetConfigPath() {
			// Check if file exists before removing
			if _, err := os.Stat(p.configPath); err == nil {
				_ = os.Remove(p.configPath)
			}
		}
	}

	if runtime.GOOS == "windows" {
		return p.cmd.Process.Kill()
	} else {
		return p.cmd.Process.Signal(syscall.SIGTERM)
	}
}

// writeCrashReport writes a crash report to the binary folder with a timestamped filename.
func writeCrashReport(m []byte) error {
	crashReportPath := config.GetBinFolderPath() + "/core_crash_" + time.Now().Format("20060102_150405") + ".log"
	return os.WriteFile(crashReportPath, m, os.ModePerm)
}

// tryFindXrayBinary attempts to locate an xray binary from common alternative
// installation paths (e.g., /usr/local/x-ui/bin/) and copies it to the expected
// binary path. This ensures the panel works even when deployed to a different
// directory than the original x-ui installation.
func tryFindXrayBinary(destPath string) bool {
	binaryName := GetBinaryName()

	// Common alternative locations where xray might be installed
	altPaths := []string{
		"/usr/local/x-ui/bin/" + binaryName,
		"/usr/local/x-ui/bin/xray",
		"/etc/x-ui/bin/" + binaryName,
		"/etc/x-ui/bin/xray",
	}

	for _, srcPath := range altPaths {
		if _, err := os.Stat(srcPath); err == nil {
			logger.Infof("Found xray binary at alternative location: %s, copying to: %s", srcPath, destPath)

			// Ensure destination directory exists
			if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
				logger.Warningf("Failed to create destination directory: %v", err)
				continue
			}

			// Read source binary
			data, err := os.ReadFile(srcPath)
			if err != nil {
				logger.Warningf("Failed to read xray binary from %s: %v", srcPath, err)
				continue
			}

			// Write to destination
			if err := os.WriteFile(destPath, data, 0o755); err != nil {
				logger.Warningf("Failed to write xray binary to %s: %v", destPath, err)
				continue
			}

			return true
		}
	}

	return false
}
