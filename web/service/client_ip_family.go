package service

import (
	"encoding/json"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/XiaSummer740/XX-UI/database"
	"github.com/XiaSummer740/XX-UI/database/model"
	"gorm.io/gorm"
)

// ClientIPFamily 描述某个客户端最近是用 IPv4 还是 IPv6 连进来的。
type ClientIPFamily struct {
	V4     int    `json:"v4"`     // 记录过的 IPv4 地址个数
	V6     int    `json:"v6"`     // 记录过的 IPv6 地址个数
	LastIP string `json:"lastIp"` // 最近一次用到的 IP
	LastAt int64  `json:"lastAt"` // 最近一次活跃时间（秒）
	Family string `json:"family"` // v4 / v6 / both
	ISP    string `json:"isp"`    // 电信 / 联通 / 移动 / 其他（按最近一次 IP 归属判断）
	ISPOrg string `json:"ispOrg"` // 原始归属信息，鼠标悬浮时显示
}

type clientIPRecord struct {
	IP        string `json:"ip"`
	Timestamp int64  `json:"timestamp"`
}

// GetClientsIPFamily 汇总每个客户端用过的 IP，判断走的是 IPv4 还是 IPv6。
// 数据来自面板自己记录的 IP 日志（inbound_client_ips 表）：
// 同一客户端既连过 v4 又连过 v6 时，Family 为 both。
func (s *InboundService) GetClientsIPFamily() (map[string]ClientIPFamily, error) {
	db := database.GetDB()
	var rows []model.InboundClientIps
	err := db.Model(&model.InboundClientIps{}).Find(&rows).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, err
	}

	result := make(map[string]ClientIPFamily, len(rows))
	for _, row := range rows {
		email := strings.TrimSpace(row.ClientEmail)
		if email == "" || strings.TrimSpace(row.Ips) == "" {
			continue
		}
		var records []clientIPRecord
		if err := json.Unmarshal([]byte(row.Ips), &records); err != nil {
			continue
		}

		// 同一地址只算一次，避免刷同一台设备被记成很多条
		v4Set := make(map[string]struct{})
		v6Set := make(map[string]struct{})
		var lastIP string
		var lastAt int64
		for _, rec := range records {
			ip := strings.TrimSpace(rec.IP)
			if ip == "" {
				continue
			}
			parsed := net.ParseIP(ip)
			if parsed == nil {
				continue
			}
			if isIPv4(parsed, ip) {
				v4Set[ip] = struct{}{}
			} else {
				v6Set[ip] = struct{}{}
			}
			if rec.Timestamp > lastAt {
				lastAt = rec.Timestamp
				lastIP = ip
			}
		}

		fam := ClientIPFamily{
			V4:     len(v4Set),
			V6:     len(v6Set),
			LastIP: lastIP,
			LastAt: lastAt,
		}
		switch {
		case fam.V4 > 0 && fam.V6 > 0:
			fam.Family = "both"
		case fam.V6 > 0:
			fam.Family = "v6"
		case fam.V4 > 0:
			fam.Family = "v4"
		default:
			continue
		}
		result[email] = fam
	}

	// 按最近一次 IP 判断运营商：并发查、带 24 小时缓存，查不到就留空
	var wg sync.WaitGroup
	var mu sync.Mutex
	sem := make(chan struct{}, 4)
	for email, fam := range result {
		if fam.LastIP == "" {
			continue
		}
		wg.Add(1)
		go func(email, ip string) {
			defer wg.Done()
			sem <- struct{}{}
			org := lookupISP(ip)
			<-sem
			if org == "" {
				return
			}
			mu.Lock()
			f := result[email]
			f.ISP = classifyISP(org)
			f.ISPOrg = org
			result[email] = f
			mu.Unlock()
		}(email, fam.LastIP)
	}
	wg.Wait()
	return result, nil
}

// isIPv4 判断地址是不是 IPv4。
// IPv4 映射地址（::ffff:1.2.3.4）在双栈监听下很常见，必须算作 IPv4。
func isIPv4(parsed net.IP, raw string) bool {
	if v4 := parsed.To4(); v4 != nil {
		return true
	}
	return !strings.Contains(raw, ":")
}

// DescribeFamily 给前端/日志用的中文说明。
func (f ClientIPFamily) DescribeFamily() string {
	switch f.Family {
	case "v4":
		return "IPv4"
	case "v6":
		return "IPv6"
	case "both":
		return "IPv4 + IPv6"
	}
	return "未知"
}

// RecentIPsFor 取某个客户端最近 n 个 IP（新的在前），给详情面板用。
func (s *InboundService) RecentIPsFor(email string, n int) []clientIPRecord {
	if n <= 0 {
		n = 10
	}
	db := database.GetDB()
	var row model.InboundClientIps
	if err := db.Where("client_email = ?", email).First(&row).Error; err != nil {
		return nil
	}
	var records []clientIPRecord
	if err := json.Unmarshal([]byte(row.Ips), &records); err != nil {
		return nil
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Timestamp > records[j].Timestamp })
	if len(records) > n {
		records = records[:n]
	}
	return records
}

var _ = time.Now
