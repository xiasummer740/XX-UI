package service

import (
	"bytes"
	crand "crypto/rand"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/XiaSummer740/XX-UI/database"
	"github.com/XiaSummer740/XX-UI/database/model"
	"github.com/XiaSummer740/XX-UI/logger"
	"github.com/XiaSummer740/XX-UI/xray"
)

// 入站体检：把「节点为什么连不上」这类问题一次性查出来，并尽量一键修复。
// 检查项覆盖：启用状态、端口监听、防火墙放行、REALITY 公钥、分享链接字段、
// 客户端 subId、xray 运行时配置、偷站目标可达性。

// 状态取值
const (
	healthOK   = "ok"
	healthWarn = "warn"
	healthFail = "fail"
	healthSkip = "skip"
)

// AuditItem 是单个检查项。
type AuditItem struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Status  string `json:"status"`
	Detail  string `json:"detail"`
	FixKey  string `json:"fixKey,omitempty"`
	Fixable bool   `json:"fixable"`
}

// InboundAudit 是单个入站的体检结果。
type InboundAudit struct {
	Id       int         `json:"id"`
	Remark   string      `json:"remark"`
	Port     int         `json:"port"`
	Protocol string      `json:"protocol"`
	Enable   bool        `json:"enable"`
	OkCount  int         `json:"okCount"`
	BadCount int         `json:"badCount"`
	Items    []AuditItem `json:"items"`
}

// derivePublicKey 由 REALITY 私钥推导公钥。
func derivePublicKey(privateKey string) (string, error) {
	if strings.TrimSpace(privateKey) == "" {
		return "", fmt.Errorf("privateKey 为空")
	}
	cmd := exec.Command(xray.GetBinaryPath(), "x25519", "-i", privateKey)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", err
	}
	for _, line := range strings.Split(out.String(), "\n") {
		// 新版: "Password (PublicKey): xxx"；旧版: "Public key: xxx"
		if i := strings.Index(line, ":"); i > 0 {
			head := strings.ToLower(line[:i])
			if strings.Contains(head, "public") || strings.Contains(head, "password") {
				return strings.TrimSpace(line[i+1:]), nil
			}
		}
	}
	return "", fmt.Errorf("无法解析 x25519 输出: %s", out.String())
}

// portListening 判断本机是否在监听该 TCP 端口。
func portListening(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 1500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// xrayRuntimePorts 读取 xray 运行时配置里实际下发的入站端口。
func xrayRuntimePorts() map[int]bool {
	ports := map[int]bool{}
	raw, err := os.ReadFile(xray.GetConfigPath())
	if err != nil {
		return ports
	}
	var cfg struct {
		Inbounds []struct {
			Port     int    `json:"port"`
			Protocol string `json:"protocol"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return ports
	}
	for _, ib := range cfg.Inbounds {
		ports[ib.Port] = true
	}
	return ports
}

// realityTargetReachable 检查 REALITY 偷站目标能否完成 TLS 握手。
func realityTargetReachable(target, serverName string) (bool, string) {
	if target == "" {
		return false, "未设置偷站目标"
	}
	addr := target
	if !strings.Contains(addr, ":") {
		addr += ":443"
	}
	sni := serverName
	if sni == "" {
		sni = strings.Split(addr, ":")[0]
	}
	dialer := &net.Dialer{Timeout: 3 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{
		ServerName:         sni,
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS12,
	})
	if err != nil {
		return false, fmt.Sprintf("TLS 握手失败: %v", err)
	}
	state := conn.ConnectionState()
	_ = conn.Close()
	if state.Version < tls.VersionTLS13 {
		return false, fmt.Sprintf("%s 不支持 TLS1.3（REALITY 要求）", sni)
	}
	return true, fmt.Sprintf("%s TLS1.3 正常", sni)
}

// CheckInboundsHealth 对全部入站做体检。
func (s *InboundService) CheckInboundsHealth() ([]*InboundAudit, error) {
	var inbounds []*model.Inbound
	if err := database.GetDB().Model(model.Inbound{}).Order("port").Find(&inbounds).Error; err != nil {
		return nil, err
	}

	// 防火墙状态只取一次
	ufwBin := ufwBinary()
	ufwStat, fwErr := ufwStatus(ufwBin)
	ufwActive := fwErr == nil && ufwIsActive(ufwStat)

	// subId 重复统计
	subIdCount := map[string]int{}
	clientsOf := map[int][]model.Client{}
	for _, ib := range inbounds {
		cl, err := s.GetClients(ib)
		if err != nil {
			continue
		}
		clientsOf[ib.Id] = cl
		for _, c := range cl {
			if c.SubID != "" {
				subIdCount[c.SubID]++
			}
		}
	}
	runtimePorts := xrayRuntimePorts()
	monitorState := s.loadNodeMonitorState()

	results := make([]*InboundAudit, 0, len(inbounds))
	for _, ib := range inbounds {
		h := &InboundAudit{
			Id:       ib.Id,
			Remark:   ib.Remark,
			Port:     ib.Port,
			Protocol: string(ib.Protocol),
			Enable:   ib.Enable,
		}

		// 1) 启用状态
		if ib.Enable {
			h.Items = append(h.Items, AuditItem{Key: "enable", Label: "入站启用", Status: healthOK, Detail: "已启用"})
		} else {
			h.Items = append(h.Items, AuditItem{
				Key: "enable", Label: "入站启用", Status: healthWarn,
				Detail: "已禁用，客户端无法连接", Fixable: true, FixKey: "enable",
			})
		}

		// 2) 端口监听
		if ib.Enable {
			if portListening(ib.Port) {
				h.Items = append(h.Items, AuditItem{Key: "listen", Label: "端口监听", Status: healthOK, Detail: fmt.Sprintf("%d 正在监听", ib.Port)})
			} else {
				h.Items = append(h.Items, AuditItem{
					Key: "listen", Label: "端口监听", Status: healthFail,
					Detail: fmt.Sprintf("本机 %d 无监听：可能 xray 未重启或入站配置有误", ib.Port),
				})
			}
		} else {
			h.Items = append(h.Items, AuditItem{Key: "listen", Label: "端口监听", Status: healthSkip, Detail: "入站已禁用，跳过"})
		}

		// 3) 防火墙
		if !ib.Enable {
			h.Items = append(h.Items, AuditItem{Key: "firewall", Label: "防火墙放行", Status: healthSkip, Detail: "入站已禁用，跳过"})
		} else if !ufwActive {
			h.Items = append(h.Items, AuditItem{Key: "firewall", Label: "防火墙放行", Status: healthOK, Detail: "未启用 ufw，无需放行"})
		} else {
			tcpOK := ufwPortAllowed(ufwStat, ib.Port, "tcp")
			udpOK := true
			if udpNeededForProtocol(string(ib.Protocol)) {
				udpOK = ufwPortAllowed(ufwStat, ib.Port, "udp")
			}
			if tcpOK && udpOK {
				h.Items = append(h.Items, AuditItem{Key: "firewall", Label: "防火墙放行", Status: healthOK, Detail: fmt.Sprintf("%d 已放行", ib.Port)})
			} else {
				missing := "tcp"
				if !udpOK {
					missing += "+udp"
				}
				h.Items = append(h.Items, AuditItem{
					Key: "firewall", Label: "防火墙放行", Status: healthFail,
					Detail: fmt.Sprintf("ufw 未放行 %d/%s", ib.Port, missing), Fixable: true, FixKey: "firewall",
				})
			}
		}

		// 4~5) REALITY 公钥 / 分享链接字段
		stream := map[string]any{}
		if strings.TrimSpace(ib.StreamSettings) != "" {
			_ = json.Unmarshal([]byte(ib.StreamSettings), &stream)
		}
		security, _ := stream["security"].(string)

		if security == "reality" {
			rs, _ := stream["realitySettings"].(map[string]any)
			privateKey, _ := rs["privateKey"].(string)
			pbk := ""
			if nested, ok := rs["settings"].(map[string]any); ok {
				pbk, _ = nested["publicKey"].(string)
			}
			derived, derr := derivePublicKey(privateKey)

			switch {
			case pbk == "" && derr == nil:
				h.Items = append(h.Items, AuditItem{
					Key: "reality_key", Label: "REALITY 公钥", Status: healthFail,
					Detail:  "settings.publicKey 缺失：分享链接里 pbk 会是空的，客户端必然连不上",
					Fixable: true, FixKey: "reality_key",
				})
			case pbk != "" && derr == nil && pbk != derived:
				h.Items = append(h.Items, AuditItem{
					Key: "reality_key", Label: "REALITY 公钥", Status: healthFail,
					Detail:  fmt.Sprintf("公钥与私钥不匹配（当前 %s… 应为 %s…）", short(pbk), short(derived)),
					Fixable: true, FixKey: "reality_key",
				})
			case pbk != "" && derr == nil:
				h.Items = append(h.Items, AuditItem{Key: "reality_key", Label: "REALITY 公钥", Status: healthOK, Detail: "公钥与私钥匹配"})
			case derr != nil:
				h.Items = append(h.Items, AuditItem{Key: "reality_key", Label: "REALITY 公钥", Status: healthWarn, Detail: "无法推导公钥：" + derr.Error()})
			}

			// 链接字段
			missing := []string{}
			if pbk == "" {
				missing = append(missing, "pbk")
			}
			if names, ok := rs["serverNames"].([]any); !ok || len(names) == 0 {
				missing = append(missing, "sni")
			}
			if ids, ok := rs["shortIds"].([]any); !ok || len(ids) == 0 {
				missing = append(missing, "sid")
			}
			if len(missing) > 0 {
				h.Items = append(h.Items, AuditItem{
					Key: "link", Label: "分享链接字段", Status: healthFail,
					Detail: "缺少 " + strings.Join(missing, "/"), Fixable: true, FixKey: "reality_key",
				})
			} else {
				h.Items = append(h.Items, AuditItem{Key: "link", Label: "分享链接字段", Status: healthOK, Detail: "pbk / sni / sid 齐全"})
			}

			// 8) 偷站目标可达性
			target, _ := rs["target"].(string)
			sni := ""
			if names, ok := rs["serverNames"].([]any); ok && len(names) > 0 {
				sni, _ = names[0].(string)
			}
			if ok, msg := realityTargetReachable(target, sni); ok {
				h.Items = append(h.Items, AuditItem{Key: "sni", Label: "偷站目标", Status: healthOK, Detail: msg})
			} else {
				h.Items = append(h.Items, AuditItem{Key: "sni", Label: "偷站目标", Status: healthWarn, Detail: msg})
			}
		}

		// 6) 客户端 subId
		emptySub, dupSub := 0, []string{}
		for _, c := range clientsOf[ib.Id] {
			if c.SubID == "" {
				emptySub++
			} else if subIdCount[c.SubID] > 1 && !containsStr(dupSub, c.SubID) {
				dupSub = append(dupSub, c.SubID)
			}
		}
		if emptySub > 0 {
			h.Items = append(h.Items, AuditItem{
				Key: "subid", Label: "订阅 ID", Status: healthWarn,
				Detail: fmt.Sprintf("%d 个客户端没有 subId（订阅里不会出现）", emptySub), Fixable: true, FixKey: "subid",
			})
		} else if len(dupSub) > 0 {
			h.Items = append(h.Items, AuditItem{
				Key: "subid", Label: "订阅 ID", Status: healthWarn,
				Detail: "重复：" + strings.Join(dupSub, ",") + "（订阅会互串）", Fixable: true, FixKey: "subid",
			})
		} else {
			h.Items = append(h.Items, AuditItem{Key: "subid", Label: "订阅 ID", Status: healthOK, Detail: "无重复、无缺失"})
		}

		// 7) xray 运行时配置
		if !ib.Enable {
			h.Items = append(h.Items, AuditItem{Key: "runtime", Label: "运行时配置", Status: healthSkip, Detail: "入站已禁用，跳过"})
		} else if runtimePorts[ib.Port] {
			h.Items = append(h.Items, AuditItem{Key: "runtime", Label: "运行时配置", Status: healthOK, Detail: "已在 xray 配置中"})
		} else {
			h.Items = append(h.Items, AuditItem{
				Key: "runtime", Label: "运行时配置", Status: healthFail,
				Detail: "xray 运行时配置里没有该端口：重启面板/xray 试试", Fixable: true, FixKey: "restart",
			})
		}

		// 9) 在线监测（由后台任务每 5 分钟探测一次）
		if e, okM := monitorState[ib.Id]; okM && e.LastCheck > 0 {
			ago := time.Since(time.Unix(e.LastCheck, 0)).Round(time.Minute)
			switch {
			case e.Fails == 0:
				h.Items = append(h.Items, AuditItem{
					Key: "monitor", Label: "在线监测", Status: healthOK,
					Detail: fmt.Sprintf("最近探测正常（%s 前）", ago),
				})
			case e.Fails < nodeMonitorFailThreshold:
				h.Items = append(h.Items, AuditItem{
					Key: "monitor", Label: "在线监测", Status: healthWarn,
					Detail: fmt.Sprintf("连续 %d 次探测失败：%s", e.Fails, e.LastError),
				})
			default:
				h.Items = append(h.Items, AuditItem{
					Key: "monitor", Label: "在线监测", Status: healthFail,
					Detail: fmt.Sprintf("连续 %d 次探测失败（已告警）：%s", e.Fails, e.LastError),
				})
			}
		} else {
			h.Items = append(h.Items, AuditItem{Key: "monitor", Label: "在线监测", Status: healthSkip, Detail: "尚未探测"})
		}

		for _, it := range h.Items {
			switch it.Status {
			case healthOK:
				h.OkCount++
			case healthFail, healthWarn:
				h.BadCount++
			}
		}
		results = append(results, h)
	}
	return results, nil
}

func short(s string) string {
	if len(s) > 6 {
		return s[:6]
	}
	return s
}

func containsStr(arr []string, v string) bool {
	for _, s := range arr {
		if s == v {
			return true
		}
	}
	return false
}

// FixInboundIssue 修复单个入站的一类问题。needRestart 为 true 时调用方需重启 xray。
func (s *InboundService) FixInboundIssue(id int, fixKey string) (msg string, needRestart bool, err error) {
	ibPtr, err := s.GetInbound(id)
	if err != nil || ibPtr == nil {
		return "", false, fmt.Errorf("入站不存在")
	}
	ib := *ibPtr

	switch fixKey {
	case "enable":
		if err := database.GetDB().Model(model.Inbound{}).Where("id = ?", id).Update("enable", true).Error; err != nil {
			return "", false, err
		}
		return "已启用入站", true, nil

	case "firewall":
		bin := ufwBinary()
		status, err := ufwStatus(bin)
		if err != nil || !ufwIsActive(status) {
			return "ufw 未启用，无需放行", false, nil
		}
		opened := false
		if !ufwPortAllowed(status, ib.Port, "tcp") {
			if _, err := ensurePort(bin, status, ib.Port, "tcp"); err != nil {
				return "", false, err
			}
			opened = true
		}
		if udpNeededForProtocol(string(ib.Protocol)) && !ufwPortAllowed(status, ib.Port, "udp") {
			if _, err := ensurePort(bin, status, ib.Port, "udp"); err != nil {
				return "", false, err
			}
			opened = true
		}
		if !opened {
			return "防火墙已放行", false, nil
		}
		return fmt.Sprintf("已放行端口 %d", ib.Port), false, nil

	case "reality_key":
		return s.fixRealityKey(ib)

	case "subid":
		return s.fixSubIds(ib)

	case "restart":
		return "需要重启 xray", true, nil
	}
	return "", false, fmt.Errorf("未知的修复类型: %s", fixKey)
}

// fixRealityKey 由私钥重新推导并写回公钥（同时补齐顶层字段，兼容旧读取路径）。
func (s *InboundService) fixRealityKey(ib model.Inbound) (string, bool, error) {
	stream := map[string]any{}
	if err := json.Unmarshal([]byte(ib.StreamSettings), &stream); err != nil {
		return "", false, err
	}
	rs, ok := stream["realitySettings"].(map[string]any)
	if !ok {
		return "", false, fmt.Errorf("不是 REALITY 入站")
	}
	privateKey, _ := rs["privateKey"].(string)
	pub, err := derivePublicKey(privateKey)
	if err != nil {
		return "", false, fmt.Errorf("推导公钥失败: %w", err)
	}

	nested, _ := rs["settings"].(map[string]any)
	if nested == nil {
		nested = map[string]any{}
	}
	if _, ok := nested["fingerprint"]; !ok {
		nested["fingerprint"] = "chrome"
	}
	if _, ok := nested["spiderX"]; !ok {
		nested["spiderX"] = "/"
	}
	nested["publicKey"] = pub
	rs["settings"] = nested
	rs["publicKey"] = pub // 顶层也写一份，兼容只读顶层的旧代码
	stream["realitySettings"] = rs

	newStream, err := json.Marshal(stream)
	if err != nil {
		return "", false, err
	}
	if err := database.GetDB().Model(model.Inbound{}).Where("id = ?", ib.Id).
		Update("stream_settings", string(newStream)).Error; err != nil {
		return "", false, err
	}
	logger.Info("入站体检：已为端口", ib.Port, "回填 REALITY 公钥")
	return fmt.Sprintf("已回填公钥 %s…", short(pub)), false, nil
}

// fixSubIds 补齐空 subId，并为重复项生成新的。
func (s *InboundService) fixSubIds(ib model.Inbound) (string, bool, error) {
	settings := map[string]any{}
	if err := json.Unmarshal([]byte(ib.Settings), &settings); err != nil {
		return "", false, fmt.Errorf("settings 解析失败: %w", err)
	}
	rawClients, _ := settings["clients"].([]any)
	changed := 0
	for i, raw := range rawClients {
		cm, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if v, _ := cm["subId"].(string); strings.TrimSpace(v) == "" {
			cm["subId"] = randomSubId()
			rawClients[i] = cm
			changed++
		}
	}
	if changed == 0 {
		return "subId 无需修复", false, nil
	}
	settings["clients"] = rawClients
	newSettings, err := json.Marshal(settings)
	if err != nil {
		return "", false, err
	}
	if err := database.GetDB().Model(model.Inbound{}).Where("id = ?", ib.Id).
		Update("settings", string(newSettings)).Error; err != nil {
		return "", false, err
	}
	return fmt.Sprintf("已补齐 %d 个 subId", changed), false, nil
}

// randomSubId 生成 16 位字母数字订阅 ID。
func randomSubId() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 16)
	if _, err := crand.Read(b); err != nil {
		seed := time.Now().UnixNano()
		for i := range b {
			seed = seed*6364136223846793005 + 1442695040888963407
			b[i] = chars[uint64(seed>>33)%uint64(len(chars))]
		}
		return string(b)
	}
	for i := range b {
		b[i] = chars[int(b[i])%len(chars)]
	}
	return string(b)
}
