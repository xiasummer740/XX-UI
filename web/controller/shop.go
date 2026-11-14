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
	g.POST("/test-bufpay", a.testBufPay)
	g.POST("/orders/approve", a.approveOrder)
	g.POST("/orders/reject", a.rejectOrder)
	g.POST("/orders/recheck", a.recheckOrder)
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

// approveOrder 面板里手动确认到账并开通（钱可能走的是静态码，BufPay 不知道）。
func (a *ShopController) approveOrder(c *gin.Context) {
	id, _ := strconv.Atoi(c.PostForm("id"))
	if id <= 0 {
		jsonObj(c, nil, fmt.Errorf("订单号不对"))
		return
	}
	var bot service.Tgbot
	bot.ApproveShopOrderFromPanel(id)
	// 备注改成「管理员确认」，否则列表里还挂着「超时未付款」的旧文案
	if err := a.shopService.MarkOrderNote(id, "管理员手动确认到账"); err != nil {
		logger.Warning("[shop] 更新订单备注失败: ", err)
	}
	order, err := a.shopService.GetOrder(id)
	jsonObj(c, order, err)
}

// rejectOrder 面板里驳回订单（没收到钱）。
func (a *ShopController) rejectOrder(c *gin.Context) {
	id, _ := strconv.Atoi(c.PostForm("id"))
	if id <= 0 {
		jsonObj(c, nil, fmt.Errorf("订单号不对"))
		return
	}
	reason := strings.TrimSpace(c.PostForm("reason"))
	if reason == "" {
		reason = "管理员驳回"
	}
	order, err := a.shopService.RejectOrder(id, 0, reason)
	jsonObj(c, order, err)
}

// recheckOrder 立即向 BufPay 查一次单：已付款就自动开通，否则返回当前状态。
func (a *ShopController) recheckOrder(c *gin.Context) {
	id, _ := strconv.Atoi(c.PostForm("id"))
	if id <= 0 {
		jsonObj(c, nil, fmt.Errorf("订单号不对"))
		return
	}
	order, err := a.shopService.GetOrder(id)
	if err != nil {
		jsonObj(c, nil, err)
		return
	}
	var buf service.BufPayService
	status, qerr := buf.QueryOrder(order.Aoid)
	if qerr != nil {
		jsonObj(c, gin.H{"status": "", "paid": false, "err": qerr.Error()}, nil)
		return
	}
	paid := service.IsPayedStatus(status)
	if paid {
		var bot service.Tgbot
		bot.ApproveShopOrderFromPanel(id)
		_ = a.shopService.MarkOrderNote(id, "BufPay 查单确认到账")
		logger.Info("[shop] 面板手动查单：订单 #", id, " BufPay 状态 ", status, "，已开通")
	}
	jsonObj(c, gin.H{"status": status, "paid": paid}, nil)
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

// testBufPay 用当前的 aid/secret 做一次真实探测（会创建一笔 0.01 元的测试单）。
func (a *ShopController) testBufPay(c *gin.Context) {
	res, err := a.shopService.TestBufPayConnection(c.PostForm("aid"), c.PostForm("secret"))
	if err != nil {
		jsonObj(c, nil, err)
		return
	}
	jsonObj(c, res, nil)
}

// BufPayNotify 是 BufPay 的到账回调（公网访问，不能要求登录，靠签名校验）。
func (a *ShopController) BufPayNotify(c *gin.Context) {
	// 入口先记一笔，方便确认 BufPay 的回调到底有没有到达
	logger.Info("[bufpay] 收到回调 from ", c.ClientIP(), " ua=", c.GetHeader("User-Agent"))
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
