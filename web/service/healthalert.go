package service

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/XiaSummer740/XX-UI/database"
	"github.com/XiaSummer740/XX-UI/database/model"
)

// 定时体检 + 主动提醒：
//   1) 每天自动跑一遍入站体检，有问题直接推 Telegram
//   2) 证书到期前提醒（默认提前 14 天，7 天内每天提醒）
//   3) 之前有流量、最近 24 小时突然零流量的节点（多半是坏了没人报）
//
// 这些结果同时存进 lastHealthReport，面板上也能看到最近一次自动体检的情况。

const (
	healthReportKey = "lastHealthReport"
	trafficPulseKey = "trafficPulseState"
	// 证书剩余天数阈值
	certWarnDays = 14
	// 零流量判定窗口（秒）
	zeroTrafficWindow = 22 * 3600
)

// HealthAlert 是自动体检发现的一个问题。
type HealthAlert struct {
	Kind    string `json:"kind"` // audit / cert / traffic
	Inbound string `json:"inbound"`
	Port    int    `json:"port"`
	Detail  string `json:"detail"`
}

// HealthReport 是最近一次自动体检的结果。
type HealthReport struct {
	At       int64         `json:"at"`
	Total    int           `json:"total"`
	BadCount int           `json:"badCount"`
	Alerts   []HealthAlert `json:"alerts"`
}

// inboundStream 只取我们需要的字段。
type inboundStream struct {
	Security    string `json:"security"`
	TlsSettings struct {
		Certificates []struct {
			CertificateFile string `json:"certificateFile"`
			Certificate     string `json:"certificate"`
		} `json:"certificates"`
	} `json:"tlsSettings"`
	RealitySettings struct {
		Settings struct {
			PublicKey string `json:"publicKey"`
		} `json:"settings"`
	} `json:"realitySettings"`
}

// certExpiry 返回入站证书的过期时间（取不到返回零值）。
func certExpiry(ib *model.Inbound) time.Time {
	var st inboundStream
	if err := json.Unmarshal([]byte(ib.StreamSettings), &st); err != nil {
		return time.Time{}
	}
	if st.Security != "tls" {
		return time.Time{}
	}
	for _, c := range st.TlsSettings.Certificates {
		var raw []byte
		if strings.TrimSpace(c.CertificateFile) != "" {
			b, err := os.ReadFile(c.CertificateFile)
			if err != nil {
				continue
			}
			raw = b
		} else if strings.TrimSpace(c.Certificate) != "" {
			raw = []byte(c.Certificate)
		}
		if len(raw) == 0 {
			continue
		}
		block, _ := pem.Decode(raw)
		if block == nil {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			continue
		}
		return cert.NotAfter
	}
	return time.Time{}
}

// RunScheduledHealthCheck 每天跑一次体检并推送结果。
func (s *InboundService) RunScheduledHealthCheck(tgbot *Tgbot) {
	var alerts []HealthAlert

	// 1) 体检
	audits, err := s.CheckInboundsHealth()
	if err == nil {
		for _, a := range audits {
			if a.BadCount == 0 || !a.Enable {
				continue
			}
			var bad []string
			for _, item := range a.Items {
				if item.Status == healthFail || item.Status == healthWarn {
					bad = append(bad, item.Label+": "+item.Detail)
				}
			}
			alert := HealthAlert{
				Kind:    "audit",
				Inbound: a.Remark,
				Port:    a.Port,
				Detail:  strings.Join(bad, "；"),
			}
			alerts = append(alerts, alert)
		}
	}

	var inbounds []*model.Inbound
	_ = database.GetDB().Model(model.Inbound{}).Find(&inbounds).Error

	now := time.Now()
	for _, ib := range inbounds {
		if !ib.Enable {
			continue
		}
		// 2) 证书到期
		if exp := certExpiry(ib); !exp.IsZero() {
			days := int(exp.Sub(now).Hours() / 24)
			if days <= certWarnDays {
				detail := fmt.Sprintf("证书还有 %d 天到期（%s）", days, exp.Format("2006-01-02"))
				if days < 0 {
					detail = fmt.Sprintf("证书已过期 %d 天（%s）", -days, exp.Format("2006-01-02"))
				}
				alerts = append(alerts, HealthAlert{
					Kind: "cert", Inbound: ib.Remark, Port: ib.Port, Detail: detail,
				})
			}
		}
	}

	// 3) 零流量节点
	alerts = append(alerts, s.checkZeroTraffic(inbounds)...)

	report := HealthReport{At: now.Unix(), Total: len(inbounds), BadCount: len(alerts), Alerts: alerts}
	if bs, err := json.Marshal(report); err == nil {
		var ss SettingService
		_ = ss.setString(healthReportKey, string(bs))
	}

	if len(alerts) == 0 || tgbot == nil {
		return
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🩺 入站体检发现 %d 个问题\n", len(alerts)))
	for _, a := range alerts {
		icon := "⚠️"
		if a.Kind == "cert" {
			icon = "📜"
		} else if a.Kind == "traffic" {
			icon = "📉"
		}
		sb.WriteString(fmt.Sprintf("\n%s %s（端口 %d）\n%s\n", icon, a.Inbound, a.Port, a.Detail))
	}
	sb.WriteString("\n可以打开面板 → 入站体检 一键修复。")
	tgbot.SendMsgToTgbotAdmins(sb.String())
}

// checkZeroTraffic 找出「之前有流量、最近突然零流量」的节点。
func (s *InboundService) checkZeroTraffic(inbounds []*model.Inbound) []HealthAlert {
	var ss SettingService
	var prev map[int]struct {
		Total int64 `json:"total"`
		At    int64 `json:"at"`
	}
	if raw, err := ss.getString(trafficPulseKey); err == nil && raw != "" {
		_ = json.Unmarshal([]byte(raw), &prev)
	}
	if prev == nil {
		prev = map[int]struct {
			Total int64 `json:"total"`
			At    int64 `json:"at"`
		}{}
	}

	now := time.Now().Unix()
	var alerts []HealthAlert
	next := map[int]struct {
		Total int64 `json:"total"`
		At    int64 `json:"at"`
	}{}

	for _, ib := range inbounds {
		total := ib.Up + ib.Down
		old, ok := prev[ib.Id]
		if ok && ib.Enable && total == old.Total && old.Total > 0 && now-old.At >= zeroTrafficWindow {
			alerts = append(alerts, HealthAlert{
				Kind:    "traffic",
				Inbound: ib.Remark,
				Port:    ib.Port,
				Detail:  fmt.Sprintf("最近 24 小时没有任何流量（累计 %.2f GB 未变化）", float64(total)/1024/1024/1024),
			})
		}
		// 有流量增长才更新基线时间，否则保留旧时间以便继续判断"还在零流量"
		if ok && total > old.Total {
			next[ib.Id] = struct {
				Total int64 `json:"total"`
				At    int64 `json:"at"`
			}{Total: total, At: now}
		} else if ok {
			next[ib.Id] = old
		} else {
			next[ib.Id] = struct {
				Total int64 `json:"total"`
				At    int64 `json:"at"`
			}{Total: total, At: now}
		}
	}

	if bs, err := json.Marshal(next); err == nil {
		_ = ss.setString(trafficPulseKey, string(bs))
	}
	return alerts
}

// HealthReportSnapshot 返回最近一次自动体检结果。
func (s *InboundService) HealthReportSnapshot() HealthReport {
	var report HealthReport
	var ss SettingService
	if raw, err := ss.getString(healthReportKey); err == nil && raw != "" {
		_ = json.Unmarshal([]byte(raw), &report)
	}
	return report
}
