package service

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/XiaSummer740/XX-UI/database"
	"github.com/XiaSummer740/XX-UI/database/model"
)

// 节点在线监控：定时对每个启用入站做一次真实握手，
// 连续失败到阈值就通过 Telegram 报一次，恢复时再报一次。
// 状态存在 settings.nodeMonitorState（按入站 ID 记录失败次数/是否已告警）。

const (
	nodeMonitorStateKey = "nodeMonitorState"
	// 连续失败多少次才告警，避免网络抖动误报
	nodeMonitorFailThreshold = 3
)

// NodeMonitorEntry 是单个入站的监控状态。
type NodeMonitorEntry struct {
	Fails     int    `json:"fails"`     // 连续失败次数
	Alerted   bool   `json:"alerted"`   // 是否已经报过警
	LastOK    int64  `json:"lastOk"`    // 最后一次成功时间（秒）
	LastCheck int64  `json:"lastCheck"` // 最后一次探测时间（秒）
	LastError string `json:"lastError"` // 最后一次失败原因
}

// NodeMonitorState 是全部入站的监控状态。
type NodeMonitorState map[int]NodeMonitorEntry

func (s *InboundService) loadNodeMonitorState() NodeMonitorState {
	state := NodeMonitorState{}
	var ss SettingService
	raw, err := ss.getString(nodeMonitorStateKey)
	if err != nil || raw == "" {
		return state
	}
	_ = json.Unmarshal([]byte(raw), &state)
	if state == nil {
		state = NodeMonitorState{}
	}
	return state
}

func (s *InboundService) saveNodeMonitorState(state NodeMonitorState) {
	bs, err := json.Marshal(state)
	if err != nil {
		return
	}
	var ss SettingService
	_ = ss.setString(nodeMonitorStateKey, string(bs))
}

// probeInbound 对入站做一次真实探测：REALITY/TLS 走 TLS 握手，其余走 TCP 连接。
func probeInbound(ib *model.Inbound) (bool, string) {
	if !ib.Enable {
		return true, ""
	}
	addr := fmt.Sprintf("127.0.0.1:%d", ib.Port)
	stream := map[string]any{}
	if strings.TrimSpace(ib.StreamSettings) != "" {
		_ = json.Unmarshal([]byte(ib.StreamSettings), &stream)
	}
	security, _ := stream["security"].(string)

	if security == "reality" || security == "tls" {
		sni := ""
		if rs, ok := stream["realitySettings"].(map[string]any); ok {
			if names, ok2 := rs["serverNames"].([]any); ok2 && len(names) > 0 {
				sni, _ = names[0].(string)
			}
		}
		if sni == "" {
			if ts, ok := stream["tlsSettings"].(map[string]any); ok {
				if names, ok2 := ts["serverNames"].([]any); ok2 && len(names) > 0 {
					sni, _ = names[0].(string)
				} else if sn, ok2 := ts["serverName"].(string); ok2 {
					sni = sn
				}
			}
		}
		if sni == "" {
			sni = "localhost"
		}
		dialer := &net.Dialer{Timeout: 4 * time.Second}
		conn, err := tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{
			ServerName:         sni,
			InsecureSkipVerify: true,
			MinVersion:         tls.VersionTLS12,
		})
		if err != nil {
			return false, "TLS 握手失败: " + err.Error()
		}
		_ = conn.Close()
		return true, ""
	}

	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		return false, "端口连接失败: " + err.Error()
	}
	_ = conn.Close()
	return true, ""
}

// RunNodeMonitor 执行一轮监控；发现异常/恢复时通过 TG 通知管理员。
func (s *InboundService) RunNodeMonitor(tgbot *Tgbot) {
	var inbounds []*model.Inbound
	if err := database.GetDB().Model(model.Inbound{}).Find(&inbounds).Error; err != nil {
		return
	}
	state := s.loadNodeMonitorState()
	now := time.Now().Unix()

	for _, ib := range inbounds {
		ok, reason := probeInbound(ib)
		entry := state[ib.Id]
		entry.LastCheck = now

		if ok {
			wasAlerted := entry.Alerted
			entry.Fails = 0
			entry.Alerted = false
			entry.LastOK = now
			entry.LastError = ""
			if wasAlerted && tgbot != nil {
				tgbot.SendMsgToTgbotAdmins(fmt.Sprintf("✅ 节点已恢复：%s（端口 %d）", ib.Remark, ib.Port))
			}
		} else {
			entry.Fails++
			entry.LastError = reason
			if entry.Fails >= nodeMonitorFailThreshold && !entry.Alerted {
				entry.Alerted = true
				if tgbot != nil {
					tgbot.SendMsgToTgbotAdmins(fmt.Sprintf(
						"❌ 节点异常：%s（端口 %d）\n连续 %d 次探测失败\n原因：%s",
						ib.Remark, ib.Port, entry.Fails, reason))
				}
			}
		}
		state[ib.Id] = entry
	}

	// 清理已删除入站的状态
	alive := map[int]bool{}
	for _, ib := range inbounds {
		alive[ib.Id] = true
	}
	for id := range state {
		if !alive[id] {
			delete(state, id)
		}
	}
	s.saveNodeMonitorState(state)
}

// NodeMonitorSnapshot 返回监控状态快照，供前端展示。
func (s *InboundService) NodeMonitorSnapshot() map[int]NodeMonitorEntry {
	return s.loadNodeMonitorState()
}
