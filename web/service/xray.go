package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"sync"

	"github.com/XiaSummer740/XX-UI/logger"
	"github.com/XiaSummer740/XX-UI/util/json_util"
	"github.com/XiaSummer740/XX-UI/xray"

	"go.uber.org/atomic"
)

var (
	p                 *xray.Process
	lock              sync.Mutex
	isNeedXrayRestart atomic.Bool // Indicates that restart was requested for Xray
	isManuallyStopped atomic.Bool // Indicates that Xray was stopped manually from the panel
	result            string
)

// XrayService provides business logic for Xray process management.
// It handles starting, stopping, restarting Xray, and managing its configuration.
type XrayService struct {
	inboundService InboundService
	settingService SettingService
	xrayAPI        xray.XrayAPI
}

// IsXrayRunning checks if the Xray process is currently running.
func (s *XrayService) IsXrayRunning() bool {
	return p != nil && p.IsRunning()
}

// GetXrayErr returns the error from the Xray process, if any.
func (s *XrayService) GetXrayErr() error {
	if p == nil {
		return nil
	}

	err := p.GetErr()
	if err == nil {
		return nil
	}

	if runtime.GOOS == "windows" && err.Error() == "exit status 1" {
		// exit status 1 on Windows means that Xray process was killed
		// as we kill process to stop in on Windows, this is not an error
		return nil
	}

	return err
}

// GetXrayResult returns the result string from the Xray process.
func (s *XrayService) GetXrayResult() string {
	if result != "" {
		return result
	}
	if s.IsXrayRunning() {
		return ""
	}
	if p == nil {
		return ""
	}

	result = p.GetResult()

	if runtime.GOOS == "windows" && result == "exit status 1" {
		// exit status 1 on Windows means that Xray process was killed
		// as we kill process to stop in on Windows, this is not an error
		return ""
	}

	return result
}

// GetXrayVersion returns the version of the running Xray process.
func (s *XrayService) GetXrayVersion() string {
	if p == nil {
		return "Unknown"
	}
	return p.GetVersion()
}

// RemoveIndex removes an element at the specified index from a slice.
// Returns a new slice with the element removed.
func RemoveIndex(s []any, index int) []any {
	return append(s[:index], s[index+1:]...)
}

// GetXrayConfig retrieves and builds the Xray configuration from settings and inbounds.
func (s *XrayService) GetXrayConfig() (*xray.Config, error) {
	templateConfig, err := s.settingService.GetXrayConfigTemplate()
	if err != nil {
		logger.Warningf("[DIAG] GetXrayConfig: GetXrayConfigTemplate failed: %v", err)
		return nil, err
	}

	xrayConfig := &xray.Config{}
	err = json.Unmarshal([]byte(templateConfig), xrayConfig)
	if err != nil {
		preview := templateConfig
		if len(preview) > 200 {
			preview = preview[:200]
		}
		logger.Warningf("[DIAG] GetXrayConfig: template unmarshal failed: %v, preview=%s", err, preview)
		return nil, err
	}

	s.inboundService.AddTraffic(nil, nil)

	inbounds, err := s.inboundService.GetAllInbounds()
	if err != nil {
		return nil, err
	}
	for _, inbound := range inbounds {
		if !inbound.Enable {
			continue
		}
		// get settings clients
		settings := map[string]any{}
		if err := json.Unmarshal([]byte(inbound.Settings), &settings); err != nil {
			preview := inbound.Settings
			if len(preview) > 200 {
				preview = preview[:200]
			}
			logger.Warningf("[DIAG] GetXrayConfig: inbound=%d Settings json.Unmarshal error: %v, preview[:200]=%s", inbound.Id, err, preview)
		}
		clients, ok := settings["clients"].([]any)
		if ok {
			// Fast O(N) lookup map for client traffic enablement
			clientStats := inbound.ClientStats
			enableMap := make(map[string]bool, len(clientStats))
			for _, clientTraffic := range clientStats {
				enableMap[clientTraffic.Email] = clientTraffic.Enable
			}

			// filter and clean clients
			var final_clients []any
			for _, client := range clients {
				c, ok := client.(map[string]any)
				if !ok {
					continue
				}

				email, _ := c["email"].(string)

				// check users active or not via stats
				if enable, exists := enableMap[email]; exists && !enable {
					logger.Infof("Remove Inbound User %s due to expiration or traffic limit", email)
					continue
				}

				// check manual disabled flag
				if manualEnable, ok := c["enable"].(bool); ok && !manualEnable {
					continue
				}

				// clear client config for additional parameters
				for key := range c {
					if key != "email" && key != "id" && key != "password" && key != "flow" && key != "method" && key != "auth" {
						delete(c, key)
					}
					if flow, ok := c["flow"].(string); ok && flow == "xtls-rprx-vision-udp443" {
						c["flow"] = "xtls-rprx-vision"
					}
				}
				final_clients = append(final_clients, any(c))
			}

			settings["clients"] = final_clients
			modifiedSettings, err := json.MarshalIndent(settings, "", "  ")
			if err != nil {
				return nil, err
			}

			inbound.Settings = string(modifiedSettings)
		}

		if len(inbound.StreamSettings) > 0 {
			// Unmarshal stream JSON
			var stream map[string]any
			if err := json.Unmarshal([]byte(inbound.StreamSettings), &stream); err != nil {
				preview := inbound.StreamSettings
				if len(preview) > 200 {
					preview = preview[:200]
				}
				logger.Warningf("[DIAG] GetXrayConfig: inbound=%d StreamSettings json.Unmarshal error: %v, preview[:200]=%s", inbound.Id, err, preview)
			}

			// Remove the "settings" field under "tlsSettings" and "realitySettings"
			tlsSettings, ok1 := stream["tlsSettings"].(map[string]any)
			realitySettings, ok2 := stream["realitySettings"].(map[string]any)
			if ok1 || ok2 {
				if ok1 {
					delete(tlsSettings, "settings")
				} else if ok2 {
					delete(realitySettings, "settings")
				}
			}

			delete(stream, "externalProxy")

			newStream, err := json.MarshalIndent(stream, "", "  ")
			if err != nil {
				return nil, err
			}
			inbound.StreamSettings = string(newStream)
		}

		// Inject chain proxy by dynamically creating an outbound and
		// adding a routing rule that sends this inbound's traffic through it.
		// NOTE: We use a routing rule (inboundTag → outboundTag) instead of
		// sockopt.dialerProxy because dialerProxy behaviour is unreliable
		// across Xray versions, especially with VLESS/TLS inbounds.
		if inbound.ChainProxy != "" && inbound.EnableChainProxy {
			var cp struct {
				Protocol string `json:"protocol"`
				Address  string `json:"address"`
				Port     int    `json:"port"`
				User     string `json:"user,omitempty"`
				Password string `json:"password,omitempty"`
			}
			if err := json.Unmarshal([]byte(inbound.ChainProxy), &cp); err == nil && cp.Address != "" && cp.Port > 0 {
				// Build a dynamic outbound for the chain proxy with a unique tag per inbound.
				// Each inbound gets its own outbound so different inbounds can use different destinations.
				outboundTag := fmt.Sprintf("chain_proxy_out_%d", inbound.Id)
				outbound := buildChainProxyOutbound(cp, outboundTag)

				// Append outbound to xrayConfig.OutboundConfigs
				var outbounds []any
				if err := json.Unmarshal([]byte(xrayConfig.OutboundConfigs), &outbounds); err == nil {
					outbounds = append(outbounds, outbound)
					if newOutbounds, err := json.Marshal(outbounds); err == nil {
						xrayConfig.OutboundConfigs = json_util.RawMessage(string(newOutbounds))
					}
				}

				// Add a routing rule: traffic from this inbound → chain_proxy_out_{id}
				var routing map[string]any
				if err := json.Unmarshal([]byte(xrayConfig.RouterConfig), &routing); err == nil {
					rules, _ := routing["rules"].([]any)
					chainRule := map[string]any{
						"type":        "field",
						"inboundTag":  []string{inbound.Tag},
						"outboundTag": outboundTag,
					}
					// Insert after the first rule (API rule) so API traffic is unaffected
					if len(rules) > 1 {
						rules = append(rules[:1], append([]any{chainRule}, rules[1:]...)...)
					} else {
						rules = append(rules, chainRule)
					}
					routing["rules"] = rules
					if newRouting, err := json.Marshal(routing); err == nil {
						xrayConfig.RouterConfig = json_util.RawMessage(string(newRouting))
					}
				}
			}
		}

		inboundConfig := inbound.GenXrayInboundConfig()
		xrayConfig.InboundConfigs = append(xrayConfig.InboundConfigs, *inboundConfig)
	}
	return xrayConfig, nil
}

// buildChainProxyOutbound builds an outbound config map for the chain proxy.
// Currently supports: socks, http.
// The returned map can be serialized to JSON and appended to xrayConfig.OutboundConfigs.
func buildChainProxyOutbound(cp struct {
	Protocol string `json:"protocol"`
	Address  string `json:"address"`
	Port     int    `json:"port"`
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"`
}, tag string) map[string]any {
	outbound := map[string]any{
		"tag":      tag,
		"protocol": cp.Protocol,
	}

	switch cp.Protocol {
	case "socks":
		server := map[string]any{
			"address": cp.Address,
			"port":    cp.Port,
		}
		if cp.User != "" {
			user := map[string]any{
				"user": cp.User,
			}
			if cp.Password != "" {
				user["pass"] = cp.Password
			}
			server["users"] = []any{user}
		}
		outbound["settings"] = map[string]any{
			"servers": []any{server},
		}
	case "http":
		server := map[string]any{
			"address": cp.Address,
			"port":    cp.Port,
		}
		if cp.User != "" {
			user := map[string]any{
				"user": cp.User,
			}
			if cp.Password != "" {
				user["pass"] = cp.Password
			}
			server["users"] = []any{user}
		}
		outbound["settings"] = map[string]any{
			"servers": []any{server},
		}
	default:
		// For unsupported protocols, fall back to SOCKS5 settings
		// to avoid generating invalid config that would crash Xray.
		logger.Warningf("[ChainProxy] Unsupported protocol %q for dynamic outbound, falling back to socks", cp.Protocol)
		outbound["protocol"] = "socks"
		server := map[string]any{
			"address": cp.Address,
			"port":    cp.Port,
		}
		if cp.User != "" {
			user := map[string]any{
				"user": cp.User,
			}
			if cp.Password != "" {
				user["pass"] = cp.Password
			}
			server["users"] = []any{user}
		}
		outbound["settings"] = map[string]any{
			"servers": []any{server},
		}
	}

	return outbound
}

// GetXrayTraffic fetches the current traffic statistics from the running Xray process.
func (s *XrayService) GetXrayTraffic() ([]*xray.Traffic, []*xray.ClientTraffic, error) {
	if !s.IsXrayRunning() {
		err := errors.New("xray is not running")
		logger.Debug("Attempted to fetch Xray traffic, but Xray is not running:", err)
		return nil, nil, err
	}
	apiPort := p.GetAPIPort()
	s.xrayAPI.Init(apiPort)
	defer s.xrayAPI.Close()

	traffic, clientTraffic, err := s.xrayAPI.GetTraffic(true)
	if err != nil {
		logger.Debug("Failed to fetch Xray traffic:", err)
		return nil, nil, err
	}
	return traffic, clientTraffic, nil
}

// RestartXray restarts the Xray process, optionally forcing a restart even if config unchanged.
func (s *XrayService) RestartXray(isForce bool) error {
	lock.Lock()
	defer lock.Unlock()
	logger.Debug("restart Xray, force:", isForce)
	isManuallyStopped.Store(false)

	xrayConfig, err := s.GetXrayConfig()
	if err != nil {
		return err
	}

	if s.IsXrayRunning() {
		if !isForce && p.GetConfig().Equals(xrayConfig) && !isNeedXrayRestart.Load() {
			logger.Debug("It does not need to restart Xray")
			return nil
		}
		p.Stop()
	}

	p = xray.NewProcess(xrayConfig)
	result = ""
	err = p.Start()
	if err != nil {
		return err
	}

	return nil
}

// StopXray stops the running Xray process.
func (s *XrayService) StopXray() error {
	lock.Lock()
	defer lock.Unlock()
	isManuallyStopped.Store(true)
	logger.Debug("Attempting to stop Xray...")
	if s.IsXrayRunning() {
		return p.Stop()
	}
	return errors.New("xray is not running")
}

// SetToNeedRestart marks that Xray needs to be restarted.
func (s *XrayService) SetToNeedRestart() {
	isNeedXrayRestart.Store(true)
}

// IsNeedRestartAndSetFalse checks if restart is needed and resets the flag to false.
func (s *XrayService) IsNeedRestartAndSetFalse() bool {
	return isNeedXrayRestart.CompareAndSwap(true, false)
}

// DidXrayCrash checks if Xray crashed by verifying it's not running and wasn't manually stopped.
func (s *XrayService) DidXrayCrash() bool {
	return !s.IsXrayRunning() && !isManuallyStopped.Load()
}
