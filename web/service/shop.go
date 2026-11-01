package service

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/XiaSummer740/XX-UI/database"
	"github.com/XiaSummer740/XX-UI/database/model"
	"github.com/XiaSummer740/XX-UI/logger"
	"github.com/google/uuid"
	tu "github.com/mymmrac/telego/telegoutil"
	"gorm.io/gorm"
)

// 流量商店：朋友在 Telegram 里自助下单，扫码付款后管理员点一下确认，
// 机器人自动在指定入站上开一个客户端，把订阅链接发过去。
//
// 计费模型：套餐 = 流量(GB) × 周期(月/季/年)，价格在面板「设置 → 流量商店」里改。

const (
	shopEnableKey  = "shopEnable"
	shopPlansKey   = "shopPlans"
	shopPayNoteKey = "shopPayNote"
	shopQrURLKey   = "shopQrUrl"
	shopQr2URLKey  = "shopQrUrl2"
	shopInboundKey = "shopInboundId"
	shopDailyKey   = "shopDailyLimit"
	shopLimitIPKey = "shopLimitIp"

	orderPending  = "pending"
	orderApproved = "approved"
	orderRejected = "rejected"

	cycleMonth   = "month"
	cycleQuarter = "quarter"
	cycleYear    = "year"
)

// ShopPlan 是一个套餐档位（流量固定，三种周期各自的单价）。
type ShopPlan struct {
	Gb      int     `json:"gb"`
	Month   float64 `json:"month"`
	Quarter float64 `json:"quarter"`
	Year    float64 `json:"year"`
}

// defaultShopPlans 是初始套餐：100 / 200 / 400 / 500 GB。
func defaultShopPlans() []ShopPlan {
	return []ShopPlan{
		{Gb: 100, Month: 10, Quarter: 27, Year: 96},
		{Gb: 200, Month: 18, Quarter: 50, Year: 180},
		{Gb: 400, Month: 32, Quarter: 90, Year: 320},
		{Gb: 500, Month: 38, Quarter: 108, Year: 380},
	}
}

// ShopService 提供商店配置与订单处理。
type ShopService struct {
	settingService SettingService
	inboundService InboundService
}

func (s *ShopService) GetShopEnable() (bool, error) {
	return s.settingService.GetShopEnable()
}

func (s *ShopService) GetShopPlans() []ShopPlan {
	var ss SettingService
	raw, err := ss.getString(shopPlansKey)
	if err != nil || strings.TrimSpace(raw) == "" {
		return defaultShopPlans()
	}
	var plans []ShopPlan
	if err := json.Unmarshal([]byte(raw), &plans); err != nil || len(plans) == 0 {
		return defaultShopPlans()
	}
	// 只保留真正在售的档位：流量要大于 0，且至少有一个周期填了价格。
	valid := make([]ShopPlan, 0, len(plans))
	for _, p := range plans {
		if p.Gb > 0 && p.HasPrice() {
			valid = append(valid, p)
		}
	}
	if len(valid) == 0 {
		return nil
	}
	return valid
}

// HasPrice 判断该档位是否至少有一个周期在售。
func (p ShopPlan) HasPrice() bool {
	return p.Month > 0 || p.Quarter > 0 || p.Year > 0
}

func (s *ShopService) SetShopPlans(plans []ShopPlan) error {
	bs, err := json.Marshal(plans)
	if err != nil {
		return err
	}
	var ss SettingService
	return ss.setString(shopPlansKey, string(bs))
}

func (s *ShopService) GetShopPayNote() string {
	var ss SettingService
	v, err := ss.getString(shopPayNoteKey)
	if err != nil {
		return ""
	}
	return v
}

func (s *ShopService) GetShopQrURL() string {
	var ss SettingService
	v, err := ss.getString(shopQrURLKey)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(v)
}

// GetShopQrURL2 支付宝收款码。
func (s *ShopService) GetShopQrURL2() string {
	var ss SettingService
	v, err := ss.getString(shopQr2URLKey)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(v)
}

func (s *ShopService) GetShopInboundID() int {
	var ss SettingService
	v, err := ss.getInt(shopInboundKey)
	if err != nil {
		return 0
	}
	return v
}

// GetShopLimitIP 每个客户端允许的设备数，默认 2。
func (s *ShopService) GetShopLimitIP() int {
	var ss SettingService
	v, err := ss.getInt(shopLimitIPKey)
	if err != nil || v < 0 {
		return 2
	}
	return v
}

func (s *ShopService) GetShopDailyLimit() int {
	var ss SettingService
	v, err := ss.getInt(shopDailyKey)
	if err != nil || v <= 0 {
		return 3
	}
	return v
}

// cycleDays 把周期换算成天数。
func cycleDays(cycle string) int {
	switch cycle {
	case cycleQuarter:
		return 90
	case cycleYear:
		return 365
	default:
		return 30
	}
}

// cycleLabel 返回中文周期名。
func cycleLabel(cycle string) string {
	switch cycle {
	case cycleQuarter:
		return "季付"
	case cycleYear:
		return "年付"
	default:
		return "月付"
	}
}

// PriceOf 返回某档位某周期的价格，找不到返回 ok=false。
func (s *ShopService) PriceOf(gb int, cycle string) (float64, bool) {
	for _, p := range s.GetShopPlans() {
		if p.Gb != gb {
			continue
		}
		switch cycle {
		case cycleQuarter:
			return p.Quarter, p.Quarter > 0
		case cycleYear:
			return p.Year, p.Year > 0
		default:
			return p.Month, p.Month > 0
		}
	}
	return 0, false
}

// FindPlan 返回指定流量的套餐。
func (s *ShopService) FindPlan(gb int) (ShopPlan, bool) {
	for _, p := range s.GetShopPlans() {
		if p.Gb == gb {
			return p, true
		}
	}
	return ShopPlan{}, false
}

// CreateOrder 下单（pending 状态，等管理员确认收款）。
func (s *ShopService) CreateOrder(chatId int64, tgName string, gb int, cycle string, payMethod string) (*model.Order, error) {
	if _, ok := s.FindPlan(gb); !ok {
		return nil, fmt.Errorf("没有这个套餐")
	}
	price, ok := s.PriceOf(gb, cycle)
	if !ok {
		return nil, fmt.Errorf("该套餐暂不支持这个周期")
	}

	db := database.GetDB()
	now := time.Now().Unix()

	// 同一个人同时只允许 1 个待确认订单
	var pending int64
	db.Model(&model.Order{}).Where("chat_id = ? AND status = ?", chatId, orderPending).Count(&pending)
	if pending > 0 {
		return nil, fmt.Errorf("你还有未确认的订单，请等管理员确认后再下单")
	}

	// 每天下单数限制，防止被刷
	dayStart := time.Now().Truncate(24 * time.Hour).Unix()
	var today int64
	db.Model(&model.Order{}).Where("chat_id = ? AND created_at >= ?", chatId, dayStart).Count(&today)
	if int(today) >= s.GetShopDailyLimit() {
		return nil, fmt.Errorf("今天的下单次数已用完（每天最多 %d 单）", s.GetShopDailyLimit())
	}

	order := &model.Order{
		ChatId:    chatId,
		TgName:    tgName,
		PlanGb:    gb,
		Cycle:     cycle,
		Days:      cycleDays(cycle),
		Price:     price,
		Status:    orderPending,
		Remark:    payMethod,
		CreatedAt: now,
	}
	if err := db.Create(order).Error; err != nil {
		return nil, err
	}
	return order, nil
}

// GetOrder 按 ID 取订单。
func (s *ShopService) GetOrder(id int) (*model.Order, error) {
	var order model.Order
	if err := database.GetDB().First(&order, id).Error; err != nil {
		return nil, err
	}
	return &order, nil
}

// ListOrders 最近的订单。
func (s *ShopService) ListOrders(limit int) ([]*model.Order, error) {
	if limit <= 0 {
		limit = 30
	}
	var orders []*model.Order
	err := database.GetDB().Order("id desc").Limit(limit).Find(&orders).Error
	return orders, err
}

// buildShopClient 生成新客户端的 JSON。
func buildShopClient(protocol model.Protocol, email string, gb int, days int, chatId int64, orderId int, limitIp int) map[string]any {
	now := time.Now()
	expiry := now.AddDate(0, 0, days).UnixMilli()
	client := map[string]any{
		"email":      email,
		"limitIp":    limitIp,
		"totalGB":    int64(gb) * 1024 * 1024 * 1024,
		"expiryTime": expiry,
		"enable":     true,
		"tgId":       chatId,
		"subId":      randomSubId(),
		"reset":      0,
		"comment":    fmt.Sprintf("bot订单#%d", orderId),
	}
	switch strings.ToLower(string(protocol)) {
	case "trojan":
		client["password"] = uuid.NewString()
	case "shadowsocks":
		client["password"] = uuid.NewString()
	case "hysteria", "hysteria2":
		client["auth"] = uuid.NewString()
	default:
		client["id"] = uuid.NewString()
		client["flow"] = ""
	}
	return client
}

// ApproveOrder 标记订单已到账并真正开通客户端，返回订单（含客户端 email）。
func (s *ShopService) ApproveOrder(id int, adminId int64) (*model.Order, error) {
	order, err := s.GetOrder(id)
	if err != nil {
		return nil, err
	}
	if order.Status != orderPending {
		return nil, fmt.Errorf("订单已处理过（当前状态：%s）", order.Status)
	}

	inboundId := s.GetShopInboundID()
	if inboundId <= 0 {
		return nil, fmt.Errorf("还没有在「设置 → 流量商店」里选择投放的入站")
	}
	inbound, err := s.inboundService.GetInbound(inboundId)
	if err != nil {
		return nil, fmt.Errorf("店铺投放的入站 #%d 已经不存在了（可能被删掉了），请到「设置 → 流量商店 → 基础设置」重新选一个投放设备", inboundId)
	}

	// 老买家重复购买：直接在原客户端上叠加流量和时长，订阅链接保持不变。
	email := fmt.Sprintf("tg%d", order.ChatId)
	_, existClient, _ := s.inboundService.GetClientByEmail(email)
	needRestart := false
	if existClient != nil {
		extended, extErr := s.extendExistingClient(inbound, email, order.PlanGb, order.Days)
		if extErr == nil && extended {
			order.Status = orderApproved
			order.ClientEmail = email
			order.HandledAt = time.Now().Unix()
			order.HandledBy = adminId
			return order, database.GetDB().Save(order).Error
		}
		// 叠加失败就退回新建，别把已付款的单卡住
	}

	clientMap := buildShopClient(inbound.Protocol, email, order.PlanGb, order.Days, order.ChatId, order.Id, s.GetShopLimitIP())
	clientJSON, err := json.Marshal(clientMap)
	if err != nil {
		return nil, err
	}
	stub := &model.Inbound{
		Id:       inboundId,
		Settings: fmt.Sprintf(`{"clients":[%s]}`, string(clientJSON)),
	}
	needRestart, err = s.inboundService.AddInboundClient(stub)
	if err != nil {
		return nil, fmt.Errorf("创建客户端失败: %v", err)
	}
	if needRestart {
		var xrayService XrayService
		xrayService.SetToNeedRestart()
	}
	order.Status = orderApproved
	order.ClientEmail = email
	order.HandledAt = time.Now().Unix()
	order.HandledBy = adminId
	if err := database.GetDB().Save(order).Error; err != nil {
		return order, err
	}
	return order, nil
}

// extendExistingClient 在已有客户端上叠加流量与时长（同一买家续费/加量，订阅链接不变）。
func (s *ShopService) extendExistingClient(inbound *model.Inbound, email string, gb int, days int) (bool, error) {
	traffic, client, err := s.inboundService.GetClientByEmail(email)
	if err != nil || client == nil || traffic == nil {
		return false, fmt.Errorf("client not found")
	}
	nowMs := time.Now().UnixMilli()
	base := client.ExpiryTime
	if base < nowMs {
		base = nowMs
	}
	client.ExpiryTime = base + int64(days)*24*60*60*1000
	client.TotalGB += int64(gb) * 1024 * 1024 * 1024
	client.Enable = true

	bs, err := json.Marshal(client)
	if err != nil {
		return false, err
	}
	stub := &model.Inbound{
		Id:       traffic.InboundId,
		Settings: fmt.Sprintf(`{"clients":[%s]}`, string(bs)),
	}

	// UpdateInboundClient 按协议需要不同的客户端标识
	var clientId string
	switch inbound.Protocol {
	case "trojan":
		clientId = client.Password
	case "shadowsocks":
		clientId = client.Email
	case "hysteria", "hysteria2":
		clientId = client.Auth
	default:
		clientId = client.ID
	}
	needRestart, err := s.inboundService.UpdateInboundClient(stub, clientId)
	if err != nil {
		return false, err
	}
	if needRestart {
		var xrayService XrayService
		xrayService.SetToNeedRestart()
	}
	return true, nil
}

// RejectOrder 拒绝订单。
func (s *ShopService) RejectOrder(id int, adminId int64, reason string) (*model.Order, error) {
	order, err := s.GetOrder(id)
	if err != nil {
		return nil, err
	}
	if order.Status != orderPending {
		return nil, fmt.Errorf("订单已处理过")
	}
	order.Status = orderRejected
	order.Remark = reason
	order.HandledAt = time.Now().Unix()
	order.HandledBy = adminId
	if err := database.GetDB().Save(order).Error; err != nil {
		return nil, err
	}
	return order, nil
}

// OrderText 把订单渲染成一段可读文字（给 telegram 用）。
func orderText(o *model.Order) string {
	txt := fmt.Sprintf("#%d｜%d GB｜%s（%d 天）｜¥%.2f\n买家：%s（%d）",
		o.Id, o.PlanGb, cycleLabel(o.Cycle), o.Days, o.Price, o.TgName, o.ChatId)
	if o.Remark != "" {
		txt += "\n支付方式：" + o.Remark
	}
	return txt
}

// PlanPriceText 生成「100 GB 月付 ¥10 / 季付 ¥27 / 年付 ¥96」这样的文案。
func planPriceText(p ShopPlan) string {
	var parts []string
	if p.Month > 0 {
		parts = append(parts, "月付 ¥"+formatPrice(p.Month))
	}
	if p.Quarter > 0 {
		parts = append(parts, "季付 ¥"+formatPrice(p.Quarter))
	}
	if p.Year > 0 {
		parts = append(parts, "年付 ¥"+formatPrice(p.Year))
	}
	return strings.Join(parts, " / ")
}

func formatPrice(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// ShopStats 经营看板数据。
type ShopStats struct {
	TodayRevenue float64         `json:"todayRevenue"`
	MonthRevenue float64         `json:"monthRevenue"`
	TotalRevenue float64         `json:"totalRevenue"`
	TodayOrders  int64           `json:"todayOrders"`
	MonthOrders  int64           `json:"monthOrders"`
	TotalOrders  int64           `json:"totalOrders"`
	Approved     int64           `json:"approved"`
	Pending      int64           `json:"pending"`
	Rejected     int64           `json:"rejected"`
	Buyers       int64           `json:"buyers"`
	Clients      int             `json:"clients"`
	UsedGB       float64         `json:"usedGb"`
	QuotaGB      float64         `json:"quotaGb"`
	Daily        []ShopDailyStat `json:"daily"`
}

// ShopDailyStat 某一天的经营数据。
type ShopDailyStat struct {
	Date    string  `json:"date"`
	Count   int64   `json:"count"`
	Revenue float64 `json:"revenue"`
}

// GetShopStats 汇总经营数据（只统计已开通的订单算收入，待确认单单独计数）。
func (s *ShopService) GetShopStats() (*ShopStats, error) {
	db := database.GetDB()
	stats := &ShopStats{}

	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).Unix()

	type agg struct {
		Cnt int64
		Sum float64
	}
	var a agg

	// 累计
	db.Model(&model.Order{}).Where("status = ?", orderApproved).
		Select("count(*) as cnt, coalesce(sum(price),0) as sum").Scan(&a)
	stats.Approved, stats.TotalRevenue = a.Cnt, a.Sum
	db.Model(&model.Order{}).Count(&stats.TotalOrders)
	db.Model(&model.Order{}).Where("status = ?", orderPending).Count(&stats.Pending)
	db.Model(&model.Order{}).Where("status = ?", orderRejected).Count(&stats.Rejected)

	// 今天
	a = agg{}
	db.Model(&model.Order{}).Where("status = ? AND created_at >= ?", orderApproved, todayStart).
		Select("count(*) as cnt, coalesce(sum(price),0) as sum").Scan(&a)
	stats.TodayOrders, stats.TodayRevenue = a.Cnt, a.Sum

	// 本月
	a = agg{}
	db.Model(&model.Order{}).Where("status = ? AND created_at >= ?", orderApproved, monthStart).
		Select("count(*) as cnt, coalesce(sum(price),0) as sum").Scan(&a)
	stats.MonthOrders, stats.MonthRevenue = a.Cnt, a.Sum

	// 买家数（去重 chat_id）
	db.Model(&model.Order{}).Where("status = ?", orderApproved).
		Distinct("chat_id").Count(&stats.Buyers)

	// 最近 7 天
	for i := 6; i >= 0; i-- {
		day := now.AddDate(0, 0, -i)
		start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location()).Unix()
		end := start + 86400
		a = agg{}
		db.Model(&model.Order{}).Where("status = ? AND created_at >= ? AND created_at < ?", orderApproved, start, end).
			Select("count(*) as cnt, coalesce(sum(price),0) as sum").Scan(&a)
		stats.Daily = append(stats.Daily, ShopDailyStat{
			Date:    day.Format("01-02"),
			Count:   a.Cnt,
			Revenue: a.Sum,
		})
	}

	// 店铺入站里的客户端用量
	inboundID := s.GetShopInboundID()
	if inboundID > 0 {
		type usage struct {
			Cnt   int
			Used  int64
			Total int64
		}
		var u usage
		db.Table("client_traffics").
			Where("inbound_id = ? AND email LIKE ?", inboundID, "tg%").
			Select("count(*) as cnt, coalesce(sum(up+down),0) as used, coalesce(sum(total),0) as total").
			Scan(&u)
		stats.Clients = u.Cnt
		stats.UsedGB = float64(u.Used) / 1073741824
		stats.QuotaGB = float64(u.Total) / 1073741824
	}

	return stats, nil
}

// ListOrdersForPanel 面板用：最近 N 条订单。
func (s *ShopService) ListOrdersForPanel(limit int) ([]model.Order, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var orders []model.Order
	err := database.GetDB().Order("id desc").Limit(limit).Find(&orders).Error
	return orders, err
}

// orderRevoked 是「已撤销」状态：误点确认后可以撤回。
const orderRevoked = "revoked"

// orderExpired 是「超时没付款、自动关闭」的状态。
const orderExpired = "expired"

// RevokeOrder 撤销一笔已开通的订单：停用该订单开通的客户端，并把订单标记为 revoked。
// 用于「买家其实没付款，管理员误点了确认」的场景。
func (s *ShopService) RevokeOrder(id int, adminId int64) (*model.Order, error) {
	order, err := s.GetOrder(id)
	if err != nil {
		return nil, err
	}
	if order.Status != orderApproved {
		return nil, fmt.Errorf("只有已开通的订单可以撤销")
	}

	// 停用客户端（不删，保留数据，方便再次开通）
	if order.ClientEmail != "" {
		traffic, client, cerr := s.inboundService.GetClientByEmail(order.ClientEmail)
		if cerr == nil && client != nil && traffic != nil {
			client.Enable = false
			if bs, merr := json.Marshal(client); merr == nil {
				stub := &model.Inbound{
					Id:       traffic.InboundId,
					Settings: fmt.Sprintf(`{"clients":[%s]}`, string(bs)),
				}
				var clientId string
				if inbound, ierr := s.inboundService.GetInbound(traffic.InboundId); ierr == nil && inbound != nil {
					switch inbound.Protocol {
					case "trojan":
						clientId = client.Password
					case "shadowsocks":
						clientId = client.Email
					case "hysteria", "hysteria2":
						clientId = client.Auth
					default:
						clientId = client.ID
					}
					if needRestart, uerr := s.inboundService.UpdateInboundClient(stub, clientId); uerr == nil {
						if needRestart {
							var xrayService XrayService
							xrayService.SetToNeedRestart()
						}
					}
				}
			}
		}
	}

	order.Status = orderRevoked
	order.HandledAt = time.Now().Unix()
	order.HandledBy = adminId
	if order.Remark == "" {
		order.Remark = "已撤销"
	} else {
		order.Remark += "（已撤销）"
	}
	if err := database.GetDB().Save(order).Error; err != nil {
		return nil, err
	}
	return order, nil
}

// ---- BufPay 自动到账 ----

// GetPayMode 返回收款模式：manual（人工确认）/ auto（BufPay 自动到账）。
func (s *ShopService) GetPayMode() string {
	mode, err := s.settingService.getString("shopPayMode")
	if err != nil || mode == "" {
		return "manual"
	}
	return mode
}

// GetBufAid 返回 BufPay 应用 aid。
func (s *ShopService) GetBufAid() string {
	aid, _ := s.settingService.getString("shopBufAid")
	return strings.TrimSpace(aid)
}

// GetBufSecret 返回 BufPay 应用 secret。
func (s *ShopService) GetBufSecret() string {
	secret, _ := s.settingService.getString("shopBufSecret")
	return strings.TrimSpace(secret)
}

// GetNotifyURL 拼出 BufPay 回调地址（必须是公网可访问的地址）。
func (s *ShopService) GetNotifyURL() string {
	domain, _ := s.settingService.GetWebDomain()
	domain = strings.TrimSpace(domain)
	if domain == "" {
		domain, _ = s.settingService.GetSubDomain()
		domain = strings.TrimSpace(domain)
	}
	port, _ := s.settingService.GetPort()
	basePath, _ := s.settingService.GetBasePath()
	if domain == "" {
		return ""
	}
	// 去掉协议前缀和端口，统一重建
	domain = strings.TrimPrefix(domain, "https://")
	domain = strings.TrimPrefix(domain, "http://")
	if i := strings.Index(domain, ":"); i > 0 {
		domain = domain[:i]
	}
	if !strings.HasPrefix(basePath, "/") {
		basePath = "/" + basePath
	}
	basePath = strings.TrimSuffix(basePath, "/")

	// 如果开了回调兜底端口（默认 80），优先用它：
	// 部分支付平台的回调服务器只肯往 80/443 发，非标准端口根本连不上。
	if httpPort := s.notifyHTTPPort(); httpPort > 0 {
		hostPort := domain
		if httpPort != 80 {
			hostPort = fmt.Sprintf("%s:%d", domain, httpPort)
		}
		return fmt.Sprintf("http://%s%s/panel/api/shop/bufpay/notify", hostPort, basePath)
	}

	return fmt.Sprintf("https://%s:%d%s/panel/api/shop/bufpay/notify", domain, port, basePath)
}

// notifyHTTPPort 返回回调兜底端口（0 表示关闭）。
func (s *ShopService) notifyHTTPPort() int {
	v, err := s.settingService.getString("shopNotifyHTTPPort")
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 || n > 65535 {
		return 0
	}
	return n
}

// GetNotifyHTTPPort 供 Web 层启动纯 HTTP 回调入口使用。
func (s *ShopService) GetNotifyHTTPPort() int {
	return s.notifyHTTPPort()
}

// VerifyBufPayNotify 校验 BufPay 回调（签名 + 金额），返回对应订单。
func (s *ShopService) VerifyBufPayNotify(aoid, orderId, orderUid, price, payPrice, sign string) (*model.Order, error) {
	var buf BufPayService
	secret := s.GetBufSecret()
	if !buf.VerifyNotify(secret, aoid, orderId, orderUid, price, payPrice, sign) {
		// 排障：把几种候选签名都算一遍，看 BufPay 到底用什么规则
		logger.Warning("[bufpay] 验签明细 ", buf.DiagnoseBufPaySign(secret, aoid, orderId, orderUid, price, payPrice, sign))
		return nil, fmt.Errorf("签名校验失败")
	}
	id, err := strconv.Atoi(strings.TrimSpace(orderId))
	if err != nil {
		return nil, fmt.Errorf("订单号异常: %s", orderId)
	}
	order, err := s.GetOrder(id)
	if err != nil {
		return nil, fmt.Errorf("没找到订单 #%s", orderId)
	}
	if !PriceEqual(NormalizePrice(order.Price), payPrice) {
		return nil, fmt.Errorf("金额不符：订单 ¥%s，实付 ¥%s", NormalizePrice(order.Price), payPrice)
	}
	return order, nil
}

// AbandonPendingOrder 作废一笔还没付款的订单（支付通道异常时用），
// 不删除记录，标记为 rejected 并写明原因，方便以后对账。
func (s *ShopService) AbandonPendingOrder(id int, reason string) error {
	order, err := s.GetOrder(id)
	if err != nil {
		return err
	}
	if order.Status != orderPending {
		return fmt.Errorf("订单已处理过")
	}
	order.Status = orderRejected
	order.Remark = reason
	order.HandledAt = time.Now().Unix()
	return database.GetDB().Save(order).Error
}

var shopReminderMu sync.Mutex
var shopRemindedAt = map[int]int64{}

// RemindPendingOrders 对长时间没人处理的待确认订单，再提醒一次管理员。
// 每笔订单最多每 30 分钟提醒一次，避免刷屏。
func (s *ShopService) RemindPendingOrders(bot *Tgbot) {
	if bot == nil {
		return
	}
	now := time.Now().Unix()
	orders := make([]*model.Order, 0)
	err := database.GetDB().
		Where("status = ? AND created_at < ?", orderPending, now-15*60).
		Order("created_at asc").
		Limit(20).
		Find(&orders).Error
	if err != nil || len(orders) == 0 {
		return
	}

	shopReminderMu.Lock()
	defer shopReminderMu.Unlock()

	for _, o := range orders {
		if last, ok := shopRemindedAt[o.Id]; ok && now-last < 30*60 {
			continue
		}
		shopRemindedAt[o.Id] = now
		waitMin := (now - o.CreatedAt) / 60
		kb := tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton("✅ 确认到账并开通").WithCallbackData(fmt.Sprintf("shop_ok %d", o.Id)),
				tu.InlineKeyboardButton("❌ 拒绝").WithCallbackData(fmt.Sprintf("shop_no %d", o.Id)),
			),
		)
		bot.SendMsgToTgbotAdmins(fmt.Sprintf(
			"⏰ <b>订单 #%d 还在等你确认</b>（已等 %d 分钟）\n\n%s\n\n"+
				"如果买家其实没付，点「拒绝」就行，他那边会收到说明；买家也可以自己取消。",
			o.Id, waitMin, orderText(o)), kb)
	}
}

var shopHealthMu sync.Mutex
var shopHealthLastWarn int64

// CheckShopHealth 检查店铺配置是否还能用（投放的入站是否还在），
// 有问题就提醒管理员。每小时最多提醒一次，避免刷屏。
func (s *ShopService) CheckShopHealth(bot *Tgbot) {
	if bot == nil {
		return
	}
	enable, err := s.settingService.GetShopEnable()
	if err != nil || !enable {
		return
	}
	inboundId := s.GetShopInboundID()
	var problem string
	switch {
	case inboundId <= 0:
		problem = "还没有选择投放的入站"
	default:
		if _, err := s.inboundService.GetInbound(inboundId); err != nil {
			problem = fmt.Sprintf("投放的入站 #%d 已经不存在了（可能被删掉了）", inboundId)
		}
	}
	if problem == "" {
		return
	}

	shopHealthMu.Lock()
	defer shopHealthMu.Unlock()
	now := time.Now().Unix()
	if now-shopHealthLastWarn < 3600 {
		return
	}
	shopHealthLastWarn = now
	bot.SendMsgToTgbotAdmins(fmt.Sprintf(
		"⚠️ <b>流量商店现在无法开通</b>\n\n%s。\n\n"+
			"买家还能看到套餐，但付款后开通会失败。\n"+
			"请到面板「设置 → 流量商店 → 基础设置」重新选择投放设备。", problem))
}

// ShopReady 判断店铺现在能不能正常卖（投放的入站还在不在）。
// 返回 false 时不要收钱，避免买家付了款却开不了号。
func (s *ShopService) ShopReady() (bool, string) {
	inboundId := s.GetShopInboundID()
	if inboundId <= 0 {
		return false, "管理员还没设置投放设备"
	}
	if _, err := s.inboundService.GetInbound(inboundId); err != nil {
		return false, fmt.Sprintf("投放的入站 #%d 已经不存在了", inboundId)
	}
	return true, ""
}

var shopBrokenMu sync.Mutex
var shopBrokenLastWarn int64

// WarnShopBroken 店铺不可用时提醒管理员，10 分钟内最多一次，避免买家狂点刷屏。
func (s *ShopService) WarnShopBroken(reason string) {
	shopBrokenMu.Lock()
	defer shopBrokenMu.Unlock()
	now := time.Now().Unix()
	if now-shopBrokenLastWarn < 600 {
		return
	}
	shopBrokenLastWarn = now
	var bot Tgbot
	bot.NewTgbot().SendMsgToTgbotAdmins("⚠️ <b>流量商店当前不可用</b>\n\n" + reason +
		"\n\n请到面板「设置 → 流量商店 → 基础设置」重新选择投放设备。买家已被拦下，没有产生扣款。")
}

// ClearOrders 清空全部订单记录（自测数据 / 重新开张时用）。
// 只删 orders 表，不动入站里的客户端。
func (s *ShopService) ClearOrders() (int64, error) {
	db := database.GetDB()
	var total int64
	if err := db.Model(&model.Order{}).Count(&total).Error; err != nil {
		return 0, err
	}
	if err := db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&model.Order{}).Error; err != nil {
		return 0, err
	}
	// 让订单号从 #1 重新开始
	db.Exec("DELETE FROM sqlite_sequence WHERE name = 'orders'")
	return total, nil
}

var shopPingMu sync.Mutex
var shopPingAt = map[int]int64{}

// PingAdminsAboutPendingOrder 买家点「付好了，查一下」但订单还没开通时，
// 给管理员推一条带一键开通按钮的消息（每单 10 分钟最多一次）。
func (s *ShopService) PingAdminsAboutPendingOrder(order *model.Order) {
	if order == nil {
		return
	}
	shopPingMu.Lock()
	now := time.Now().Unix()
	last, seen := shopPingAt[order.Id]
	if seen && now-last < 600 {
		shopPingMu.Unlock()
		return
	}
	shopPingAt[order.Id] = now
	shopPingMu.Unlock()

	var bot Tgbot
	bot.PingAdminsPendingOrder(order)
}

// SaveOrderAoid 记录 BufPay 的订单号，便于主动查单对账。
func (s *ShopService) SaveOrderAoid(id int, aoid string) error {
	return database.GetDB().Model(&model.Order{}).Where("id = ?", id).
		Update("aoid", strings.TrimSpace(aoid)).Error
}

// ReconcilePendingOrders 主动向 BufPay 查单：已经付了款但回调没送到的订单，
// 在这里自动补开通。每单查一次，不依赖 BufPay 的回调（回调经常丢）。
//
// 除了「待确认」，也会复查最近 24 小时「已超时关闭」的订单：
// 万一买家在关单后才付款，钱真到了就得给人家开通。
func (s *ShopService) ReconcilePendingOrders(bot *Tgbot) {
	if bot == nil {
		return
	}
	if s.GetPayMode() != "auto" {
		return
	}
	orders := make([]*model.Order, 0)
	err := database.GetDB().
		Where("status IN (?, ?) AND aoid <> '' AND created_at > ?", orderPending, orderExpired, time.Now().Unix()-24*3600).
		Order("id asc").Limit(20).Find(&orders).Error
	if err != nil || len(orders) == 0 {
		return
	}

	var buf BufPayService
	for _, o := range orders {
		status, qerr := buf.QueryOrder(o.Aoid)
		if qerr != nil {
			s.noteQueryFailure(qerr)
			continue
		}
		s.noteQuerySuccess()
		if !IsPayedStatus(status) {
			continue
		}
		logger.Info("[shop] 主动查单发现订单 #", o.Id, " 已付款（", status, "），自动开通")
		bot.AutoApproveShopOrder(o.Id)
	}
}

// PendingTTL 返回未付款订单的自动关闭时长，默认 3 分钟（面板可调）。
func (s *ShopService) PendingTTL() time.Duration {
	v, err := s.settingService.getString("shopPendingTTL")
	if err != nil {
		return 3 * time.Minute
	}
	return parsePendingTTL(v)
}

// parsePendingTTL 把面板里填的分钟数解析成时长，非法值回落到 3 分钟。
func parsePendingTTL(raw string) time.Duration {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 || n > 240 {
		return 3 * time.Minute
	}
	return time.Duration(n) * time.Minute
}

// ExpirePendingOrder 关掉一笔超时未付款的订单，释放「每人只能有 1 个待确认订单」的名额。
func (s *ShopService) ExpirePendingOrder(id int) error {
	return database.GetDB().Model(&model.Order{}).
		Where("id = ? AND status = ?", id, orderPending).
		Updates(map[string]any{
			"status":     orderExpired,
			"remark":     "超时未付款，自动关闭",
			"handled_at": time.Now().Unix(),
		}).Error
}

// ExpireStalePendingOrders 把超过 TTL 还没付款的 BufPay 订单自动关闭。
// 关之前一定先查一次单：万一人家刚好付了，就直接开通 —— 绝不能把付了钱的单作废。
func (s *ShopService) ExpireStalePendingOrders(bot *Tgbot) {
	if bot == nil || s.GetPayMode() != "auto" {
		return
	}
	ttl := s.PendingTTL()
	deadline := time.Now().Add(-ttl).Unix()
	orders := make([]*model.Order, 0)
	err := database.GetDB().
		Where("status = ? AND aoid <> '' AND created_at < ?", orderPending, deadline).
		Order("id asc").Limit(20).Find(&orders).Error
	if err != nil || len(orders) == 0 {
		return
	}

	var buf BufPayService
	for _, o := range orders {
		status, qerr := buf.QueryOrder(o.Aoid)
		if qerr != nil {
			// 查不到就别乱关，下一轮再试
			s.noteQueryFailure(qerr)
			continue
		}
		s.noteQuerySuccess()
		if IsPayedStatus(status) {
			logger.Info("[shop] 超时单 #", o.Id, " 其实已付款（", status, "），直接开通")
			bot.AutoApproveShopOrder(o.Id)
			continue
		}
		if err := s.ExpirePendingOrder(o.Id); err != nil {
			logger.Warning("[shop] 关闭超时订单失败 #", o.Id, ": ", err)
			continue
		}
		logger.Info("[shop] 订单 #", o.Id, " 超过 ", ttl, " 未付款，已自动关闭")
		bot.NotifyShopOrderExpired(o)
	}
}

// 查单连续失败计数：BufPay 抽风时能及时告诉管理员，而不是让订单一直卡着。
var (
	shopQueryMu      sync.Mutex
	shopQueryFails   int
	shopQueryLastErr string
)

func (s *ShopService) noteQueryFailure(err error) {
	shopQueryMu.Lock()
	shopQueryFails++
	n := shopQueryFails
	if err != nil {
		shopQueryLastErr = err.Error()
	}
	last := shopQueryLastErr
	shopQueryMu.Unlock()

	logger.Debugf("[shop] 查单失败（连续 %d 次）: %s", n, last)
	if n == 5 && last != "" {
		s.WarnShopBroken("BufPay 查单连续失败 5 次（订单可能开通不了）：" + last)
	}
}

func (s *ShopService) noteQuerySuccess() {
	shopQueryMu.Lock()
	shopQueryFails = 0
	shopQueryLastErr = ""
	shopQueryMu.Unlock()
}

// GetOrderByAoid 按 BufPay 的订单号查本地订单。
func (s *ShopService) GetOrderByAoid(aoid string) (*model.Order, error) {
	aoid = strings.TrimSpace(aoid)
	if aoid == "" {
		return nil, fmt.Errorf("aoid 为空")
	}
	order := &model.Order{}
	if err := database.GetDB().Where("aoid = ?", aoid).First(order).Error; err != nil {
		return nil, err
	}
	return order, nil
}

// TestBufPayConnection 用给定的 aid/secret 做一次真实探测：
// 先看网络能不能到 BufPay，再试着下一笔 0.01 元的测试单，验证签名/额度/二维码是否正常。
func (s *ShopService) TestBufPayConnection(aid, secret string) (map[string]string, error) {
	aid = strings.TrimSpace(aid)
	secret = strings.TrimSpace(secret)
	if aid == "" {
		aid = s.GetBufAid()
	}
	if secret == "" {
		secret = s.GetBufSecret()
	}
	if aid == "" || secret == "" {
		return nil, fmt.Errorf("请先填写应用 aid 和 app secret")
	}

	var buf BufPayService
	result := map[string]string{"aid": aid}

	// 第 1 步：网络连通性（查一个不存在的单，正常会返回 not_exist）
	if _, err := buf.QueryOrder("00000000000000000000000000000000"); err != nil {
		return nil, fmt.Errorf("连不上 BufPay：%v（先检查服务器 DNS 和出网）", err)
	}
	result["network"] = "ok"

	// 第 2 步：真实下单验证签名
	testId := fmt.Sprintf("xxui-test-%d", time.Now().Unix())
	pay, err := buf.CreatePayment(aid, secret, "连接测试", "alipay", "0.01", testId, "0", s.GetNotifyURL())
	if err != nil {
		return nil, fmt.Errorf("下单失败：%v", err)
	}
	result["order"] = testId
	result["aoid"] = pay.Aoid
	if pay.QrImg != "" || pay.Qr != "" {
		result["qr"] = "ok"
	} else {
		result["qr"] = "missing"
	}
	return result, nil
}
