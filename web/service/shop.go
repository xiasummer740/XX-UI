package service

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/XiaSummer740/XX-UI/database"
	"github.com/XiaSummer740/XX-UI/database/model"
	"github.com/google/uuid"
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
func (s *ShopService) CreateOrder(chatId int64, tgName string, gb int, cycle string) (*model.Order, error) {
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
		return nil, fmt.Errorf("读取入站失败: %v", err)
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
	return fmt.Sprintf("#%d｜%d GB｜%s（%d 天）｜¥%.2f\n买家：%s（%d）",
		o.Id, o.PlanGb, cycleLabel(o.Cycle), o.Days, o.Price, o.TgName, o.ChatId)
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
