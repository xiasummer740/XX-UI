package service

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os/exec"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/XiaSummer740/XX-UI/database"
	"github.com/XiaSummer740/XX-UI/database/model"
	"github.com/XiaSummer740/XX-UI/logger"
)

// 三网延迟监测
//
// 面板所在机器自己 ping 国内三大运营商的公共测速点，记录「延迟 / 丢包 / 抖动」。
// 这是判断「这条线路对国内用户好不好」最直接的数据：
//   - 延迟低但丢包高  → 线路拥塞 / 被 QOS
//   - 某个运营商整体偏慢 → 回程绕路
//   - 突然整体掉到 100% → 出口挂了
//
// 数据每轮一行（城市 × 运营商），默认 5 分钟一轮，保留 30 天。

const (
	// 每轮每个测点发几个包。5 个包足够算丢包率，又不会把对端打疼。
	latencyPingCount = 5
	// 单个测点整体超时
	latencyProbeTimeout = 12 * time.Second
	// 同时探测的分组数
	latencyProbeConcurrency = 4
	// 曲线最多返回多少个点（前端画图用，多了没意义）
	latencyMaxSeriesPoints = 600
)

// LatencyTarget 是一个「城市 + 运营商」以及它的候选测点。
// 候选是为了容错：国内测点经常封禁 ICMP 或临时故障，探测时按顺序取第一个通的。
type LatencyTarget struct {
	City       string   `json:"city"`
	Isp        string   `json:"isp"`
	Candidates []string `json:"candidates"`
}

// defaultLatencyTargets 是内置的三网测点（2026-11 在中国大陆实测可通）。
//
// 选点原则：优先用运营商自己的公共 DNS / 骨干节点，ICMP 常年开放；
// 每格尽量配 2 个候选，主用挂了自动切备用。
var defaultLatencyTargets = []LatencyTarget{
	// 深圳
	{City: "深圳", Isp: "电信", Candidates: []string{"202.96.134.133", "58.60.188.1"}},
	{City: "深圳", Isp: "联通", Candidates: []string{"210.21.196.6", "119.145.0.1"}},
	{City: "深圳", Isp: "移动", Candidates: []string{"211.136.192.6", "120.196.0.1"}},
	// 北京
	{City: "北京", Isp: "电信", Candidates: []string{"123.125.81.6", "219.141.147.210"}},
	{City: "北京", Isp: "联通", Candidates: []string{"123.123.123.123", "202.106.195.68"}},
	{City: "北京", Isp: "移动", Candidates: []string{"211.136.17.107", "211.140.0.1"}},
	// 上海
	{City: "上海", Isp: "电信", Candidates: []string{"61.152.81.193", "202.96.209.133"}},
	{City: "上海", Isp: "联通", Candidates: []string{"140.207.1.1", "210.22.70.3"}},
	{City: "上海", Isp: "移动", Candidates: []string{"211.136.112.200", "120.196.0.1"}},
	// 成都
	{City: "成都", Isp: "电信", Candidates: []string{"61.139.2.69", "171.221.0.1"}},
	{City: "成都", Isp: "联通", Candidates: []string{"119.6.6.6", "113.56.60.1"}},
	{City: "成都", Isp: "移动", Candidates: []string{"211.137.96.205", "218.205.0.1"}},
}

// latencyChosen 缓存每格当前可用的测点，避免每轮都去试所有候选。
var (
	latencyChosen   = map[string]string{}
	latencyChosenMu sync.Mutex
	// latencyRunMu 保证同一时刻只有一轮探测在跑（定时任务与「立即测」不重叠）
	latencyRunMu sync.Mutex
)

// errLatencyBusy 表示上一轮还没跑完。
var errLatencyBusy = errors.New("上一轮探测还没跑完，稍等几秒")

// LatencyService 负责探测、存取、聚合三网延迟数据。
type LatencyService struct {
	settingService SettingService
}

// LatencyProbeResult 是一次 ping 的结果。
type LatencyProbeResult struct {
	Target  string  `json:"target"`
	Sent    int     `json:"sent"`
	Recv    int     `json:"recv"`
	LossPct float64 `json:"lossPct"`
	MinMs   float64 `json:"minMs"`
	AvgMs   float64 `json:"avgMs"`
	MaxMs   float64 `json:"maxMs"`
	Jitter  float64 `json:"jitter"`
}

// LatencyPoint 是曲线上的一个点（桶内聚合后的值）。
type LatencyPoint struct {
	Ts      int64   `json:"ts"`
	AvgMs   float64 `json:"avgMs"`
	P95Ms   float64 `json:"p95Ms"`
	MaxMs   float64 `json:"maxMs"`
	LossPct float64 `json:"lossPct"`
	Jitter  float64 `json:"jitter"`
}

// LatencyDaily 是某一天的统计（按 +08:00 切天）。
type LatencyDaily struct {
	Day   string  `json:"day"` // 2026-11-07
	Ts    int64   `json:"ts"`  // 当天 00:00（unix 秒），画横轴用
	AvgMs float64 `json:"avgMs"`
	P95Ms float64 `json:"p95Ms"`
	P99Ms float64 `json:"p99Ms"`
	MaxMs float64 `json:"maxMs"`
}

// LatencyCell 是「某个城市某个运营商」在查询窗口内的汇总。
type LatencyCell struct {
	City        string  `json:"city"`
	Isp         string  `json:"isp"`
	Target      string  `json:"target"`
	LastTs      int64   `json:"lastTs"`
	LastAvgMs   float64 `json:"lastAvgMs"`
	LastLossPct float64 `json:"lastLossPct"`
	AvgMs       float64 `json:"avgMs"`
	P50Ms       float64 `json:"p50Ms"`
	P95Ms       float64 `json:"p95Ms"`
	P99Ms       float64 `json:"p99Ms"`
	MinMs       float64 `json:"minMs"`
	MaxMs       float64 `json:"maxMs"`
	LossPct     float64 `json:"lossPct"`
	AvgJitter   float64 `json:"avgJitter"`
	Samples     int     `json:"samples"`
	// 探针失败 / 慢请求计数（对应 Grafana 那种 Probe failures / >300ms count 面板）
	FailCount int            `json:"failCount"`
	Slow300   int            `json:"slow300"`
	Slow500   int            `json:"slow500"`
	Slow1000  int            `json:"slow1000"`
	Series    []LatencyPoint `json:"series"`
	Daily     []LatencyDaily `json:"daily"`
	// 基线：过去 7 天同一小时的均值（按 +08:00 的小时归槽，24 个点）
	Baseline []LatencyBaseline `json:"baseline,omitempty"`
}

// LatencyBaseline 是「过去 7 天同一小时」的均值，用来画常态基线。
type LatencyBaseline struct {
	Hour  int     `json:"hour"`
	AvgMs float64 `json:"avgMs"`
}

// LatencySummary 是整块卡片的数据。
type LatencySummary struct {
	Hours         int           `json:"hours"`
	UpdatedAt     int64         `json:"updatedAt"` // 最近一轮探测时间
	Enabled       bool          `json:"enabled"`
	Interval      int           `json:"interval"`      // 探测间隔（分钟）
	RetentionDays int           `json:"retentionDays"` // 保留天数
	Cities        []string      `json:"cities"`
	Isps          []string      `json:"isps"`
	Cells         []LatencyCell `json:"cells"`
}

var (
	rePingLoss = regexp.MustCompile(`(\d+(?:\.\d+)?)% packet loss`)
	rePingTxRx = regexp.MustCompile(`(\d+) packets transmitted, (\d+) (?:packets )?received`)
	rePingRtt  = regexp.MustCompile(`=\s*([\d.]+)/([\d.]+)/([\d.]+)/([\d.]+)`)

	// Windows ping 的输出格式（开发机上跑单测用，生产是 Linux）
	reWinTxRx = regexp.MustCompile(`Sent = (\d+), Received = (\d+), Lost = (\d+)`)
	reWinRtt  = regexp.MustCompile(`Minimum = (\d+)ms, Maximum = (\d+)ms, Average = (\d+)ms`)
)

// PingOnce 对一个地址做一次 ping，返回丢包与延迟统计。
// 用系统的 ping 而不是手撸 ICMP：走的是同一套系统配置（IPv4/IPv6、路由、策略路由），
// 和运维自己敲命令看到的结果一致。
func (s *LatencyService) PingOnce(ctx context.Context, ip string, count int) LatencyProbeResult {
	if count <= 0 {
		count = latencyPingCount
	}

	cctx, cancel := context.WithTimeout(ctx, latencyProbeTimeout)
	defer cancel()

	var args []string
	if runtime.GOOS == "windows" {
		args = []string{"-n", strconv.Itoa(count), "-w", "2000", ip}
	} else {
		args = []string{"-n", "-q", "-c", strconv.Itoa(count), "-i", "0.3", "-W", "2", ip}
	}
	cmd := exec.CommandContext(cctx, "ping", args...)
	out, _ := cmd.CombinedOutput()
	return parsePingOutput(string(out), ip, count, runtime.GOOS == "windows")
}

// parsePingOutput 把 ping 的输出解析成结构化结果。
// 抽成纯函数是为了能拿真实输出（Debian iputils / Windows）写单测，不必真的联网。
func parsePingOutput(text, ip string, count int, windows bool) LatencyProbeResult {
	res := LatencyProbeResult{Target: ip, Sent: count}
	if count <= 0 {
		count = latencyPingCount
		res.Sent = count
	}

	if windows {
		if m := reWinTxRx.FindStringSubmatch(text); len(m) == 4 {
			res.Sent, _ = strconv.Atoi(m[1])
			res.Recv, _ = strconv.Atoi(m[2])
			lost, _ := strconv.Atoi(m[3])
			if res.Sent > 0 {
				res.LossPct = float64(lost) / float64(res.Sent) * 100
			}
		}
		if m := reWinRtt.FindStringSubmatch(text); len(m) == 4 {
			res.MinMs, _ = strconv.ParseFloat(m[1], 64)
			res.MaxMs, _ = strconv.ParseFloat(m[2], 64)
			res.AvgMs, _ = strconv.ParseFloat(m[3], 64)
		}
	} else {
		lossParsed := false
		if m := rePingLoss.FindStringSubmatch(text); len(m) == 2 {
			res.LossPct, _ = strconv.ParseFloat(m[1], 64)
			lossParsed = true
		}
		if m := rePingTxRx.FindStringSubmatch(text); len(m) == 3 {
			res.Sent, _ = strconv.Atoi(m[1])
			res.Recv, _ = strconv.Atoi(m[2])
		} else if lossParsed && res.Sent > 0 {
			res.Recv = int(math.Round(float64(res.Sent) * (1 - res.LossPct/100)))
		}
		if m := rePingRtt.FindStringSubmatch(text); len(m) == 5 {
			res.MinMs, _ = strconv.ParseFloat(m[1], 64)
			res.AvgMs, _ = strconv.ParseFloat(m[2], 64)
			res.MaxMs, _ = strconv.ParseFloat(m[3], 64)
			res.Jitter, _ = strconv.ParseFloat(m[4], 64)
		}
	}

	// ping 完全跑不起来（没装 / 参数不兼容）：当成全丢包，前端会显示「—」
	if res.Sent <= 0 {
		res.Sent = count
	}
	if strings.Contains(text, "unknown host") || strings.Contains(text, "Name or service not known") {
		res.Recv = 0
		res.LossPct = 100
	}
	return res
}

// Targets 返回当前生效的测点列表：设置里配了 latencyTargets 就用它，否则用内置默认。
func (s *LatencyService) Targets() []LatencyTarget {
	raw, err := s.settingService.getString("latencyTargets")
	if err == nil && strings.TrimSpace(raw) != "" {
		var custom []LatencyTarget
		if err := json.Unmarshal([]byte(raw), &custom); err == nil && len(custom) > 0 {
			cleaned := make([]LatencyTarget, 0, len(custom))
			for _, t := range custom {
				if strings.TrimSpace(t.City) == "" || strings.TrimSpace(t.Isp) == "" || len(t.Candidates) == 0 {
					continue
				}
				t.City = strings.TrimSpace(t.City)
				t.Isp = strings.TrimSpace(t.Isp)
				cleaned = append(cleaned, t)
			}
			if len(cleaned) > 0 {
				return cleaned
			}
		} else if err != nil {
			logger.Warning("latencyTargets 配置解析失败，回落到内置测点:", err)
		}
	}
	return defaultLatencyTargets
}

// probeTarget 按候选顺序找一个能通（收到包）的测点。
func (s *LatencyService) probeTarget(ctx context.Context, t LatencyTarget) (LatencyProbeResult, bool) {
	key := t.City + "|" + t.Isp

	latencyChosenMu.Lock()
	cached := latencyChosen[key]
	latencyChosenMu.Unlock()

	// 先试缓存的那个，通了就不再折腾
	if cached != "" {
		res := s.PingOnce(ctx, cached, latencyPingCount)
		if res.Recv > 0 {
			return res, true
		}
	}

	for _, ip := range t.Candidates {
		if ip == cached {
			continue
		}
		res := s.PingOnce(ctx, ip, latencyPingCount)
		if res.Recv > 0 {
			latencyChosenMu.Lock()
			latencyChosen[key] = ip
			latencyChosenMu.Unlock()
			return res, true
		}
	}
	// 全都不通：仍然记录一条全丢包，保留「这一格挂了」的事实
	res := s.PingOnce(ctx, t.Candidates[0], latencyPingCount)
	return res, false
}

// RunOnce 跑一轮完整探测（所有城市 × 运营商），把结果写库。
// 同一时刻只允许一轮在跑：定时任务和「立即测」撞一起时不会打出两份重复数据。
func (s *LatencyService) RunOnce() (int, error) {
	if !latencyRunMu.TryLock() {
		return 0, errLatencyBusy
	}
	defer latencyRunMu.Unlock()

	targets := s.Targets()
	now := time.Now().Unix()

	type outcome struct {
		t   LatencyTarget
		res LatencyProbeResult
	}
	results := make([]outcome, 0, len(targets))

	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, latencyProbeConcurrency)
	ctx := context.Background()

	for _, t := range targets {
		wg.Add(1)
		go func(t LatencyTarget) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			res, _ := s.probeTarget(ctx, t)
			mu.Lock()
			results = append(results, outcome{t: t, res: res})
			mu.Unlock()
		}(t)
	}
	wg.Wait()

	samples := make([]model.LatencySample, 0, len(results))
	for _, o := range results {
		samples = append(samples, model.LatencySample{
			City:    o.t.City,
			Isp:     o.t.Isp,
			Target:  o.res.Target,
			Ts:      now,
			Sent:    o.res.Sent,
			Recv:    o.res.Recv,
			LossPct: round2(o.res.LossPct),
			MinMs:   round2(o.res.MinMs),
			AvgMs:   round2(o.res.AvgMs),
			MaxMs:   round2(o.res.MaxMs),
			Jitter:  round2(o.res.Jitter),
		})
	}
	if len(samples) == 0 {
		return 0, nil
	}
	if err := database.GetDB().Create(&samples).Error; err != nil {
		return 0, err
	}
	return len(samples), nil
}

// Prune 删除早于保留期的样本，返回删除行数。
func (s *LatencyService) Prune(retentionDays int) (int64, error) {
	if retentionDays <= 0 {
		retentionDays = 30
	}
	cutoff := time.Now().Add(-time.Duration(retentionDays) * 24 * time.Hour).Unix()
	res := database.GetDB().Where("ts < ?", cutoff).Delete(&model.LatencySample{})
	return res.RowsAffected, res.Error
}

// LatencyMaxRows 是历史表行数上限。
// 间隔调到 1 秒时一天就有 100 万行，靠保留天数兜不住，用行数硬上限封顶。
const LatencyMaxRows = 600000

// TrimToMaxRows 超过上限时按 id 从老到新删，只留最新的 maxRows 行。
func (s *LatencyService) TrimToMaxRows(maxRows int) (int64, error) {
	if maxRows <= 0 {
		return 0, nil
	}
	var count int64
	if err := database.GetDB().Model(&model.LatencySample{}).Count(&count).Error; err != nil {
		return 0, err
	}
	if count <= int64(maxRows) {
		return 0, nil
	}
	excess := count - int64(maxRows)
	var ids []int64
	if err := database.GetDB().Model(&model.LatencySample{}).
		Order("id asc").Limit(int(excess)).Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	res := database.GetDB().Where("id IN ?", ids).Delete(&model.LatencySample{})
	return res.RowsAffected, res.Error
}

// Summary 汇总最近 hours 小时的数据。
func (s *LatencyService) Summary(hours int) (*LatencySummary, error) {
	return s.SummaryRange(hours, 0, 0)
}

// SummaryRange 汇总指定区间（unix 秒）。from/to 都 > 0 且 from < to 时优先于 hours，
// 供前端框选缩放用；否则退回「最近 hours 小时」。
func (s *LatencyService) SummaryRange(hours int, from, to int64) (*LatencySummary, error) {
	enabled, interval, retention, _ := s.settingService.GetLatencyConfig()
	if hours <= 0 {
		hours = 24
	}
	if hours > retention {
		hours = retention
	}

	since := time.Now().Add(-time.Duration(hours) * time.Hour).Unix()
	until := int64(0)
	if from > 0 && to > 0 && to > from {
		since = from
		until = to
		hours = int((to - from + 3599) / 3600)
		if hours <= 0 {
			hours = 1
		}
	}
	var rows []model.LatencySample
	sum := &LatencySummary{
		Hours:         hours,
		Enabled:       enabled,
		Interval:      interval,
		RetentionDays: retention,
		Cities:        []string{},
		Isps:          []string{},
		Cells:         []LatencyCell{},
	}

	q := database.GetDB().Model(&model.LatencySample{}).Where("ts >= ?", since)
	if until > 0 {
		q = q.Where("ts <= ?", until)
	}
	if err := q.Order("ts asc").Find(&rows).Error; err != nil {
		return nil, err
	}

	type bucket struct {
		avg  []float64
		loss []float64
		rows []model.LatencySample
		city string
		isp  string
		sent int
		recv int
	}
	buckets := map[string]*bucket{}
	citySeen := map[string]bool{}
	ispSeen := map[string]bool{}

	for _, r := range rows {
		key := r.City + "|" + r.Isp
		b, ok := buckets[key]
		if !ok {
			b = &bucket{city: r.City, isp: r.Isp}
			buckets[key] = b
		}
		b.rows = append(b.rows, r)
		b.sent += r.Sent
		b.recv += r.Recv
		if r.Recv > 0 {
			b.avg = append(b.avg, r.AvgMs)
		}
		b.loss = append(b.loss, r.LossPct)
		if r.Ts > sum.UpdatedAt {
			sum.UpdatedAt = r.Ts
		}
		citySeen[r.City] = true
		ispSeen[r.Isp] = true
	}

	// 城市 / 运营商顺序按内置默认来，界面才稳定
	for _, t := range s.Targets() {
		if !citySeen[t.City] {
			continue
		}
		added := false
		for _, c := range sum.Cities {
			if c == t.City {
				added = true
				break
			}
		}
		if !added {
			sum.Cities = append(sum.Cities, t.City)
		}
		if !ispSeen[t.Isp] {
			continue
		}
		added = false
		for _, i := range sum.Isps {
			if i == t.Isp {
				added = true
				break
			}
		}
		if !added {
			sum.Isps = append(sum.Isps, t.Isp)
		}
	}

	for _, t := range s.Targets() {
		key := t.City + "|" + t.Isp
		b, ok := buckets[key]
		cell := LatencyCell{City: t.City, Isp: t.Isp, Series: []LatencyPoint{}}
		if !ok || len(b.rows) == 0 {
			sum.Cells = append(sum.Cells, cell)
			continue
		}

		last := b.rows[len(b.rows)-1]
		cell.Target = last.Target
		cell.LastTs = last.Ts
		cell.LastAvgMs = last.AvgMs
		cell.LastLossPct = last.LossPct
		cell.Samples = len(b.rows)
		cell.MinMs = last.MinMs
		cell.MaxMs = last.MaxMs
		if b.sent > 0 {
			cell.LossPct = round2(float64(b.sent-b.recv) / float64(b.sent) * 100)
		}

		// 逐轮统计：失败次数、慢请求计数、抖动均值
		var jitters []float64
		for _, r := range b.rows {
			if r.Recv == 0 {
				cell.FailCount++
			}
			if r.AvgMs > 300 {
				cell.Slow300++
			}
			if r.AvgMs > 500 {
				cell.Slow500++
			}
			if r.AvgMs > 1000 {
				cell.Slow1000++
			}
			if r.Recv > 0 && r.Jitter > 0 {
				jitters = append(jitters, r.Jitter)
			}
		}
		if len(jitters) > 0 {
			cell.AvgJitter = round2(avgOf(jitters))
		}

		if len(b.avg) > 0 {
			sorted := append([]float64(nil), b.avg...)
			sort.Float64s(sorted)
			cell.AvgMs = round2(avgOf(sorted))
			cell.MinMs = round2(sorted[0])
			cell.MaxMs = round2(sorted[len(sorted)-1])
			cell.P50Ms = round2(percentile(sorted, 50))
			cell.P95Ms = round2(percentile(sorted, 95))
			cell.P99Ms = round2(percentile(sorted, 99))
		}

		// 曲线：按桶聚合（桶内取平均 / P95 / 最大 / 丢包 / 抖动）
		cell.Series = bucketize(b.rows, seriesBucketCount(len(b.rows), latencyMaxSeriesPoints))
		// 按天统计（+08:00 切天）
		cell.Daily = dailyStats(b.rows)

		sum.Cells = append(sum.Cells, cell)
	}


	// 基线：过去 7 天「同一小时」的均值（24 个槽），只在看 24h 这类短窗口时算，
	// 一次 GROUP BY 只回 12×24 行，代价可控。
	if hours <= 26 && len(sum.Cells) > 0 {
		now := time.Now().Unix()
		type baseRow struct {
			City  string
			Isp   string
			Hour  int
			AvgMs float64
		}
		var brs []baseRow
		hourExpr := "CAST(strftime('%H', ts + 28800, 'unixepoch') AS INTEGER)"
		err := database.GetDB().Model(&model.LatencySample{}).
			Select("city, isp, "+hourExpr+" AS hour, AVG(avg_ms) AS avg_ms").
			Where("ts >= ? AND ts < ?", now-7*86400, now-86400).
			Group("city, isp, " + hourExpr).
			Scan(&brs).Error
		if err == nil && len(brs) > 0 {
			byCell := map[string][]LatencyBaseline{}
			for _, r := range brs {
				key := r.City + "|" + r.Isp
				byCell[key] = append(byCell[key], LatencyBaseline{Hour: r.Hour, AvgMs: r.AvgMs})
			}
			for i := range sum.Cells {
				key := sum.Cells[i].City + "|" + sum.Cells[i].Isp
				if v, ok := byCell[key]; ok {
					sort.Slice(v, func(a, b int) bool { return v[a].Hour < v[b].Hour })
					sum.Cells[i].Baseline = v
				}
			}
		}
	}

	return sum, nil
}

// latencyCST 用固定 +08:00 切天：服务器本身多半是 UTC，
// 但「每日平均」这种图必须按国内用户看到的日期算。
var latencyCST = time.FixedZone("CST", 8*3600)

// seriesBucketCount 决定把 N 个样本压成几个点：
// 样本少就一个不压，样本多就等分成最多 max 个桶。
func seriesBucketCount(n, max int) int {
	if n <= max {
		return n
	}
	return max
}

// bucketize 把样本按时间等分成 buckets 个桶，桶内求平均 / P95 / 最大 / 丢包 / 抖动。
// 比「每 N 个取一个」好：既不会把尖峰整根丢掉，曲线也不会毛刺。
func bucketize(rows []model.LatencySample, buckets int) []LatencyPoint {
	if len(rows) == 0 || buckets <= 0 {
		return []LatencyPoint{}
	}
	if buckets > len(rows) {
		buckets = len(rows)
	}
	points := make([]LatencyPoint, 0, buckets)
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
		chunk := rows[start:end]

		avgs := make([]float64, 0, len(chunk))
		var lossSum, jitterSum float64
		var jitterN, sent, recv int
		var maxMs float64
		for _, r := range chunk {
			if r.Recv > 0 {
				avgs = append(avgs, r.AvgMs)
			}
			if r.MaxMs > maxMs {
				maxMs = r.MaxMs
			}
			lossSum += r.LossPct
			if r.Jitter > 0 && r.Recv > 0 {
				jitterSum += r.Jitter
				jitterN++
			}
			sent += r.Sent
			recv += r.Recv
		}

		p := LatencyPoint{
			Ts:      chunk[len(chunk)-1].Ts,
			LossPct: 0,
			MaxMs:   round2(maxMs),
		}
		if sent > 0 {
			p.LossPct = round2(float64(sent-recv) / float64(sent) * 100)
		}
		if len(avgs) > 0 {
			sorted := append([]float64(nil), avgs...)
			sort.Float64s(sorted)
			p.AvgMs = round2(avgOf(sorted))
			p.P95Ms = round2(percentile(sorted, 95))
		}
		if jitterN > 0 {
			p.Jitter = round2(jitterSum / float64(jitterN))
		}
		points = append(points, p)
	}
	return points
}

// dailyStats 按 +08:00 切天，算出每天的 平均 / P95 / P99 / 最大。
func dailyStats(rows []model.LatencySample) []LatencyDaily {
	if len(rows) == 0 {
		return []LatencyDaily{}
	}
	byDay := map[string][]float64{}
	maxByDay := map[string]float64{}
	tsByDay := map[string]int64{}
	order := make([]string, 0)
	for _, r := range rows {
		t := time.Unix(r.Ts, 0).In(latencyCST)
		day := t.Format("2006-01-02")
		if _, ok := byDay[day]; !ok {
			order = append(order, day)
			midnight := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, latencyCST)
			tsByDay[day] = midnight.Unix()
		}
		if r.Recv > 0 {
			byDay[day] = append(byDay[day], r.AvgMs)
		}
		if r.MaxMs > maxByDay[day] {
			maxByDay[day] = r.MaxMs
		}
	}
	out := make([]LatencyDaily, 0, len(order))
	for _, day := range order {
		vals := byDay[day]
		d := LatencyDaily{Day: day, Ts: tsByDay[day], MaxMs: round2(maxByDay[day])}
		if len(vals) > 0 {
			sorted := append([]float64(nil), vals...)
			sort.Float64s(sorted)
			d.AvgMs = round2(avgOf(sorted))
			d.P95Ms = round2(percentile(sorted, 95))
			d.P99Ms = round2(percentile(sorted, 99))
		}
		out = append(out, d)
	}
	return out
}
func avgOf(sorted []float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	var sum float64
	for _, v := range sorted {
		sum += v
	}
	return sum / float64(len(sorted))
}

// percentile 用线性插值取百分位（输入必须已升序）。
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 100 {
		return sorted[len(sorted)-1]
	}
	pos := (p / 100) * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return sorted[lo]
	}
	frac := pos - float64(lo)
	return sorted[lo] + (sorted[hi]-sorted[lo])*frac
}

func round2(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return math.Round(v*100) / 100
}
