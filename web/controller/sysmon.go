package controller

import (
	"strconv"

	"github.com/XiaSummer740/XX-UI/web/service"

	"github.com/gin-gonic/gin"
)

// SysmonController 提供服务器自身指标（CPU/内存/磁盘/网络/进程/连接数）的历史曲线。
type SysmonController struct {
	BaseController
	sysmonService service.SysmonService
}

// NewSysmonController 创建控制器并注册路由。
func NewSysmonController(g *gin.RouterGroup) *SysmonController {
	a := &SysmonController{}
	a.initRouter(g)
	return a
}

func (a *SysmonController) initRouter(g *gin.RouterGroup) {
	g.POST("/summary", a.summary)
}

// summary 返回最近 N 小时的服务器指标曲线。
func (a *SysmonController) summary(c *gin.Context) {
	hours := 24
	if v := c.PostForm("hours"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			hours = n
		}
	}
	sum, err := a.sysmonService.Summary(hours)
	jsonObj(c, sum, err)
}
