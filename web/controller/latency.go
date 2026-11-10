package controller

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/XiaSummer740/XX-UI/logger"
	"github.com/XiaSummer740/XX-UI/web/service"

	"github.com/gin-gonic/gin"
)

// LatencyController 提供三网延迟监测的数据接口。
type LatencyController struct {
	BaseController
	latencyService service.LatencyService
	settingService service.SettingService
}

// NewLatencyController 创建控制器并注册路由。
func NewLatencyController(g *gin.RouterGroup) *LatencyController {
	a := &LatencyController{}
	a.initRouter(g)
	return a
}

func (a *LatencyController) initRouter(g *gin.RouterGroup) {
	// 用 POST：查询窗口参数走 body，避免出现在访问日志/Referer 里
	g.POST("/summary", a.summary)
	g.POST("/run", a.runNow)
	g.POST("/targets", a.targets)
	g.POST("/compare", a.compare)
	g.POST("/slo", a.slo)
}

// summary 返回最近 N 小时的三网延迟汇总（城市 × 运营商 + 曲线）。
func (a *LatencyController) summary(c *gin.Context) {
	hours := 24
	if v := c.PostForm("hours"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			hours = n
		}
	}
	// 框选缩放时前端会带 from/to（unix 秒），优先于 hours
	var from, to int64
	if v := c.PostForm("from"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			from = n
		}
	}
	if v := c.PostForm("to"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			to = n
		}
	}
	mode := strings.TrimSpace(c.PostForm("mode"))
	sum, err := a.latencyService.SummaryRange(hours, from, to, mode)
	// 排障用：把请求窗口和实际返回跨度记一行，7d/30d 不生效时能直接看出来
	if err == nil && sum != nil {
		spanH := 0.0
		if len(sum.Cells) > 0 && len(sum.Cells[0].Series) > 1 {
			s := sum.Cells[0].Series
			spanH = float64(s[len(s)-1].Ts-s[0].Ts) / 3600
		}
		logger.Infof("[latency] summary hours=%d from=%d to=%d mode=%s -> 返回跨度 %.1fh 点数 %d",
			hours, from, to, mode, spanH, len(sum.Cells))
	}
	jsonObj(c, sum, err)
}

// runNow 立刻跑一轮探测（点「立即测」时调用）。
func (a *LatencyController) runNow(c *gin.Context) {
	n, err := a.latencyService.RunOnce()
	if err != nil {
		jsonMsg(c, "立即探测失败（上一轮可能还没跑完，稍等几秒）", err)
		return
	}
	if n == 0 {
		jsonMsg(c, "立即探测失败", http.ErrBodyNotAllowed)
		return
	}
	sum, err := a.latencyService.Summary(24)
	jsonObj(c, gin.H{"count": n, "summary": sum}, err)
}

// compare 对每个测点同时跑 ICMP 和 TCP，用来分辨「真拥塞」和「ICMP 被限速」。
func (a *LatencyController) compare(c *gin.Context) {
	start := time.Now()
	rows := a.latencyService.Compare(context.Background())
	jsonObj(c, gin.H{"rows": rows, "tookMs": time.Since(start).Milliseconds()}, nil)
}

// slo 返回每日可用率趋势 + 本月 SLO 错误预算。
func (a *LatencyController) slo(c *gin.Context) {
	days := 30
	if v := c.PostForm("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 90 {
			days = n
		}
	}
	target := 99.5
	if v := c.PostForm("target"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 && f < 100 {
			target = f
		}
	}
	sum, err := a.latencyService.SloSummary(days, target)
	jsonObj(c, sum, err)
}

// targets 返回当前生效的测点配置（前端「设置」弹窗用来回填）。
func (a *LatencyController) targets(c *gin.Context) {
	enabled, interval, retention, raw := a.settingService.GetLatencyConfig()
	jsonObj(c, gin.H{
		"enabled":       enabled,
		"interval":      interval,
		"retentionDays": retention,
		"targetsJson":   raw,
		"targets":       a.latencyService.Targets(),
		"mode":          a.latencyService.ProbeMode(),
	}, nil)
}
