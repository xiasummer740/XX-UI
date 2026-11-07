package controller

import (
	"net/http"
	"strconv"

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
}

// summary 返回最近 N 小时的三网延迟汇总（城市 × 运营商 + 曲线）。
func (a *LatencyController) summary(c *gin.Context) {
	hours := 24
	if v := c.PostForm("hours"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			hours = n
		}
	}
	sum, err := a.latencyService.Summary(hours)
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

// targets 返回当前生效的测点配置（前端「设置」弹窗用来回填）。
func (a *LatencyController) targets(c *gin.Context) {
	enabled, interval, retention, raw := a.settingService.GetLatencyConfig()
	jsonObj(c, gin.H{
		"enabled":       enabled,
		"interval":      interval,
		"retentionDays": retention,
		"targetsJson":   raw,
		"targets":       a.latencyService.Targets(),
	}, nil)
}
