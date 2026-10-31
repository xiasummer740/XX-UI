package controller

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/XiaSummer740/XX-UI/logger"
	"github.com/XiaSummer740/XX-UI/web/service"
)

// ShopController 流量商店的经营数据接口（面板用）+ BufPay 支付回调。
type ShopController struct {
	shopService service.ShopService
}

// NewShopController 创建控制器并注册路由。
func NewShopController(g *gin.RouterGroup) *ShopController {
	a := &ShopController{}
	a.initRouter(g)
	return a
}

// initRouter 注册 /panel/api/shop/* 路由（挂在需要登录的 api 组下）。
func (a *ShopController) initRouter(g *gin.RouterGroup) {
	g.GET("/stats", a.getStats)
	g.GET("/orders", a.getOrders)
	g.POST("/clear-orders", a.clearOrders)
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

// clearOrders 清空全部订单记录（自测数据）。
func (a *ShopController) clearOrders(c *gin.Context) {
	n, err := a.shopService.ClearOrders()
	if err != nil {
		jsonObj(c, nil, err)
		return
	}
	jsonObj(c, gin.H{"deleted": n}, nil)
}

// BufPayNotify 是 BufPay 的到账回调（公网访问，不能要求登录，靠签名校验）。
func (a *ShopController) BufPayNotify(c *gin.Context) {
	aoid := c.PostForm("aoid")
	orderId := c.PostForm("order_id")
	orderUid := c.PostForm("order_uid")
	price := c.PostForm("price")
	payPrice := c.PostForm("pay_price")
	sign := c.PostForm("sign")

	order, err := a.shopService.VerifyBufPayNotify(aoid, orderId, orderUid, price, payPrice, sign)
	if err != nil {
		// 排障：把 BufPay 真实回调的参数打出来（签名候选在 service 层打印）
		_ = c.Request.ParseForm()
		keys := make([]string, 0, len(c.Request.PostForm))
		for k := range c.Request.PostForm {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var detail strings.Builder
		for _, k := range keys {
			detail.WriteString(fmt.Sprintf("%s=%q ", k, c.Request.PostForm.Get(k)))
		}
		logger.Warning("[bufpay] 回调校验失败: ", err, " | 参数: ", detail.String())
		// 兜底：订单号和金额都对得上时，提醒管理员手动确认，别因为验签失败丢单
		if id, perr := strconv.Atoi(strings.TrimSpace(orderId)); perr == nil {
			if o, ge := a.shopService.GetOrder(id); ge == nil && o.Status == "pending" &&
				service.PriceEqual(service.NormalizePrice(o.Price), payPrice) {
				var bot service.Tgbot
				bot.NotifyShopSuspiciousNotify(o, detail.String())
				c.String(http.StatusOK, "success")
				return
			}
		}
		c.String(http.StatusBadRequest, "fail")
		return
	}

	if order.Status != "pending" {
		// 已处理过（重复回调），直接回 success 让 BufPay 停止重试
		c.String(http.StatusOK, "success")
		return
	}

	var tgBot service.Tgbot
	tgBot.AutoApproveShopOrder(order.Id)
	c.String(http.StatusOK, "success")
}
