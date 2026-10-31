package controller

import (
	"github.com/gin-gonic/gin"

	"github.com/XiaSummer740/XX-UI/web/service"
)

// ShopController 流量商店的经营数据接口（面板用）。
type ShopController struct {
	shopService service.ShopService
}

// NewShopController 创建控制器并注册路由。
func NewShopController(g *gin.RouterGroup) *ShopController {
	a := &ShopController{}
	a.initRouter(g)
	return a
}

// initRouter 注册 /panel/shop/* 路由。
func (a *ShopController) initRouter(g *gin.RouterGroup) {
	g.GET("/stats", a.getStats)
	g.GET("/orders", a.getOrders)
}

// getStats 返回经营看板数据。
func (a *ShopController) getStats(c *gin.Context) {
	stats, err := a.shopService.GetShopStats()
	jsonObj(c, stats, err)
}

// getOrders 返回最近订单列表。
func (a *ShopController) getOrders(c *gin.Context) {
	orders, err := a.shopService.ListOrdersForPanel(200)
	jsonObj(c, orders, err)
}
