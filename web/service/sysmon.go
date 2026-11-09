package service

import (
	"math"
	"runtime"
	"time"

	"github.com/XiaSummer740/XX-UI/database"
	"github.com/XiaSummer740/XX-UI/database/model"
	"github.com/XiaSummer740/XX-UI/util/sys"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	psnet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
)

// SysRetentionDays 系统指标保留天数（30 秒一个点，7 天约 2 万行）。
const SysRetentionDays = 7

// SysPoint 是图上的一个点。
type SysPoint struct {
	Ts    int64   `json:"ts"`
	Value float64 `json:"value"`
}

// SysSeries 是一条指标曲线。
type SysSeries struct {
	Key    string     `json:"key"`
	Name   string     `json:"name"`
	Unit   string     `json:"unit"`
	Last   float64    `json:"last"`
	Max    float64    `json:"max"`
	Avg    float64    `json:"avg"`
	Points []SysPoint `json:"points"`
}

// SysSummary 是整块「服务器状态」卡片的数据。
type SysSummary struct {
	UpdatedAt int64       `json:"updatedAt"`
	Samples   int         `json:"samples"`
	Series    []SysSeries `json:"series"`
}

// SysmonService 采集并汇总服务器自身指标。
type SysmonService struct{}

var (
	sysLastIn   uint64
	sysLastOut  uint64
	sysLastTime time.Time
)

// Collect 采一次指标并落库。网络速率用相邻两次的字节差算。
func (s *SysmonService) Collect() error {
	now := time.Now()
	rec := model.SysSample{Ts: now.Unix()}

	if pct, err := cpu.Percent(0, false); err == nil && len(pct) > 0 {
		rec.Cpu = round2(pct[0])
	}
	if avg, err := load.Avg(); err == nil {
		rec.Load1, rec.Load5, rec.Load15 = round2(avg.Load1), round2(avg.Load5), round2(avg.Load15)
	}
	if vm, err := mem.VirtualMemory(); err == nil {
		rec.MemPct = round2(vm.UsedPercent)
	}
	if sw, err := mem.SwapMemory(); err == nil {
		rec.SwapPct = round2(sw.UsedPercent)
	}
	if du, err := disk.Usage("/"); err == nil {
		rec.DiskPct = round2(du.UsedPercent)
	}
	if io, err := psnet.IOCounters(false); err == nil && len(io) > 0 {
		in, out := io[0].BytesRecv, io[0].BytesSent
		if !sysLastTime.IsZero() {
			secs := now.Sub(sysLastTime).Seconds()
			if secs > 0 {
				rec.NetIn = round2(float64(in-sysLastIn) / secs)
				rec.NetOut = round2(float64(out-sysLastOut) / secs)
			}
		}
		sysLastIn, sysLastOut, sysLastTime = in, out, now
	}
	if pids, err := process.Pids(); err == nil {
		rec.Procs = len(pids)
	} else {
		rec.Procs = runtime.NumCPU()
	}
	if n, err := sys.GetTCPCount(); err == nil {
		rec.Tcp = n
	}
	if n, err := sys.GetUDPCount(); err == nil {
		rec.Udp = n
	}
	return database.GetDB().Create(&rec).Error
}

// Prune 删掉保留期之前的数据。
func (s *SysmonService) Prune(days int) (int64, error) {
	if days <= 0 {
		days = SysRetentionDays
	}
	cutoff := time.Now().AddDate(0, 0, -days).Unix()
	res := database.GetDB().Where("ts < ?", cutoff).Delete(&model.SysSample{})
	return res.RowsAffected, res.Error
}

// Summary 返回最近 hours 小时的各条曲线（抽样到最多 240 个点）。
func (s *SysmonService) Summary(hours int) (*SysSummary, error) {
	if hours <= 0 {
		hours = 24
	}
	since := time.Now().Add(-time.Duration(hours) * time.Hour).Unix()
	var rows []model.SysSample
	if err := database.GetDB().Where("ts >= ?", since).Order("ts asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	sum := &SysSummary{Samples: len(rows), Series: []SysSeries{}}
	if len(rows) == 0 {
		return sum, nil
	}
	sum.UpdatedAt = rows[len(rows)-1].Ts

	buckets := sampleBuckets(len(rows), 240)

	pick := func(fn func(model.SysSample) float64) []SysPoint {
		pts := make([]SysPoint, 0, buckets)
		size := float64(len(rows)) / float64(buckets)
		for i := 0; i < buckets; i++ {
			start := int(math.Floor(float64(i) * size))
			end := int(math.Floor(float64(i+1) * size))
			if end > len(rows) {
				end = len(rows)
			}
			if start >= end {
				continue
			}
			var sumV float64
			for _, r := range rows[start:end] {
				sumV += fn(r)
			}
			pts = append(pts, SysPoint{Ts: rows[end-1].Ts, Value: round2(sumV / float64(end-start))})
		}
		return pts
	}

	add := func(key, name, unit string, fn func(model.SysSample) float64) {
		pts := pick(fn)
		var mx, total float64
		for _, p := range pts {
			total += p.Value
			if p.Value > mx {
				mx = p.Value
			}
		}
		last := 0.0
		if len(pts) > 0 {
			last = pts[len(pts)-1].Value
		}
		avg := 0.0
		if len(pts) > 0 {
			avg = total / float64(len(pts))
		}
		sum.Series = append(sum.Series, SysSeries{
			Key: key, Name: name, Unit: unit,
			Last: round2(last), Max: round2(mx), Avg: round2(avg), Points: pts,
		})
	}

	add("cpu", "CPU 使用率", "%", func(r model.SysSample) float64 { return r.Cpu })
	add("load1", "负载 1 分钟", "", func(r model.SysSample) float64 { return r.Load1 })
	add("mem", "内存使用率", "%", func(r model.SysSample) float64 { return r.MemPct })
	add("swap", "Swap 使用率", "%", func(r model.SysSample) float64 { return r.SwapPct })
	add("disk", "磁盘使用率", "%", func(r model.SysSample) float64 { return r.DiskPct })
	add("netIn", "下行", "B/s", func(r model.SysSample) float64 { return r.NetIn })
	add("netOut", "上行", "B/s", func(r model.SysSample) float64 { return r.NetOut })
	add("procs", "进程数", "", func(r model.SysSample) float64 { return float64(r.Procs) })
	add("tcp", "TCP 连接", "", func(r model.SysSample) float64 { return float64(r.Tcp) })
	add("udp", "UDP 连接", "", func(r model.SysSample) float64 { return float64(r.Udp) })

	return sum, nil
}

// sampleBuckets 控制点数：太少没形，太多手机端发糊。
func sampleBuckets(n, max int) int {
	if n <= max {
		return n
	}
	return max
}
