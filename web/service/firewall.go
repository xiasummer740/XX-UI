package service

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/XiaSummer740/XX-UI/database"
	"github.com/XiaSummer740/XX-UI/database/model"
	"github.com/XiaSummer740/XX-UI/logger"
)

// x-ui 只管生成 xray 配置，不会去动系统防火墙。
// 于是常见问题是：入站起来了、xray 也在监听，但 ufw/iptables 没放行该端口，
// 外部客户端连不上（面板里看不出任何异常）。这个文件负责把端口放行做掉。
//
// 设计原则：
//   - 只加不减：删除入站时不自动关闭端口，避免误伤共用同一端口的其它服务；
//   - 幂等：先读 ufw 状态，已有规则就跳过；
//   - 失败不阻断：防火墙操作出错只记日志，不影响入站本身的创建。

// fwResult 是同步防火墙的汇总结果
type fwResult struct {
	FirewallActive bool     `json:"firewallActive"`
	FirewallName   string   `json:"firewallName"`
	Opened         []string `json:"opened"`
	AlreadyOpen    []string `json:"alreadyOpen"`
	Failed         []string `json:"failed"`
	Message        string   `json:"message"`
}

// ufwBinary 返回 ufw 可执行文件路径，找不到返回空串。
// systemd 启动的服务 PATH 往往很短，所以优先用绝对路径。
func ufwBinary() string {
	for _, p := range []string{"/usr/sbin/ufw", "/sbin/ufw", "/usr/bin/ufw", "/bin/ufw"} {
		if _, err := exec.LookPath(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath("ufw"); err == nil {
		return p
	}
	return ""
}

// ufwStatus 返回 ufw status 的原始输出。
func ufwStatus(bin string) (string, error) {
	out, err := exec.Command(bin, "status").Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// ufwIsActive 判断 ufw 是否处于启用状态。
func ufwIsActive(status string) bool {
	return strings.Contains(status, "Status: active")
}

// ufwPortAllowed 检查某个 TCP/UDP 端口是否已经在 ufw 里放行。
// 按第一列做精确匹配，避免 443 命中 20443 这种子串误判。
func ufwPortAllowed(status string, port int, proto string) bool {
	want := strconv.Itoa(port) + "/" + proto
	for _, line := range strings.Split(status, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// 形如 "8443/tcp (v6)" 的规则
		to := strings.TrimSuffix(strings.TrimSpace(fields[0]), "(v6)")
		if to != want {
			continue
		}
		if strings.Contains(strings.ToUpper(strings.Join(fields[1:], " ")), "ALLOW") {
			return true
		}
	}
	return false
}

// udpNeededForProtocol 判断该协议的入站是否需要同时放行 UDP。
func udpNeededForProtocol(protocol string) bool {
	switch protocol {
	case "hysteria", "hysteria2", "tuic", "wireguard":
		return true
	}
	return false
}

// listensPublicly 判断入站是否对外监听（空 / 0.0.0.0 / :: 视为对外）。
func listensPublicly(listen string) bool {
	switch strings.TrimSpace(listen) {
	case "", "0.0.0.0", "::", "::0":
		return true
	}
	return false
}

// ensurePort 在 ufw 里放行单个端口，已放行则跳过。返回 (是否新加了规则, 错误)。
func ensurePort(bin, status string, port int, proto string) (bool, error) {
	if ufwPortAllowed(status, port, proto) {
		return false, nil
	}
	spec := fmt.Sprintf("%d/%s", port, proto)
	cmd := exec.Command(bin, "allow", spec, "comment", "x-ui auto")
	if out, err := cmd.CombinedOutput(); err != nil {
		return false, fmt.Errorf("ufw allow %s 失败: %v (%s)", spec, err, strings.TrimSpace(string(out)))
	}
	return true, nil
}

// openFirewallForInboundPort 在新建入站后尽力放行其端口。
// 任何失败都只记日志，不影响入站创建。
func (s *InboundService) openFirewallForInboundPort(inbound *model.Inbound) {
	if inbound == nil || !inbound.Enable || inbound.Port <= 0 || !listensPublicly(inbound.Listen) {
		return
	}
	bin := ufwBinary()
	if bin == "" {
		logger.Debug("firewall: 未找到 ufw，跳过端口放行 port=", inbound.Port)
		return
	}
	status, err := ufwStatus(bin)
	if err != nil || !ufwIsActive(status) {
		logger.Debug("firewall: ufw 未启用，跳过端口放行 port=", inbound.Port)
		return
	}
	protos := []string{"tcp"}
	if udpNeededForProtocol(string(inbound.Protocol)) {
		protos = append(protos, "udp")
	}
	for _, proto := range protos {
		opened, err := ensurePort(bin, status, inbound.Port, proto)
		if err != nil {
			logger.Warning("firewall: 放行端口失败 port=", inbound.Port, " proto=", proto, " err=", err)
			continue
		}
		if opened {
			logger.Info("firewall: 已自动放行端口 ", inbound.Port, "/", proto)
		}
	}
}

// SyncFirewallPorts 扫描所有启用的入站，把未放行的端口补齐。
// 供面板「通用操作 → 同步防火墙端口」使用。
func (s *InboundService) SyncFirewallPorts() (*fwResult, error) {
	res := &fwResult{FirewallName: "ufw"}
	bin := ufwBinary()
	if bin == "" {
		res.Message = "系统未安装 ufw，未做任何修改（如需自动放行请手动配置防火墙）"
		return res, nil
	}
	status, err := ufwStatus(bin)
	if err != nil {
		return nil, fmt.Errorf("读取 ufw 状态失败: %v", err)
	}
	if !ufwIsActive(status) {
		res.Message = "防火墙未启用，端口本来就可达，无需放行"
		return res, nil
	}
	res.FirewallActive = true

	var inbounds []*model.Inbound
	if err := database.GetDB().Model(model.Inbound{}).Where("enable = ?", true).Find(&inbounds).Error; err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	for _, ib := range inbounds {
		if ib.Port <= 0 || !listensPublicly(ib.Listen) {
			continue
		}
		protos := []string{"tcp"}
		if udpNeededForProtocol(string(ib.Protocol)) {
			protos = append(protos, "udp")
		}
		for _, proto := range protos {
			spec := fmt.Sprintf("%d/%s", ib.Port, proto)
			if seen[spec] {
				continue
			}
			seen[spec] = true
			if ufwPortAllowed(status, ib.Port, proto) {
				res.AlreadyOpen = append(res.AlreadyOpen, spec)
				continue
			}
			opened, err := ensurePort(bin, status, ib.Port, proto)
			if err != nil || !opened {
				res.Failed = append(res.Failed, spec)
				continue
			}
			res.Opened = append(res.Opened, spec)
			// 放行后需要刷新一次状态，避免重复添加
			if st, err2 := ufwStatus(bin); err2 == nil {
				status = st
			}
		}
	}

	if len(res.Opened) == 0 {
		res.Message = fmt.Sprintf("全部 %d 个端口都已放行，无需改动", len(res.AlreadyOpen))
	} else {
		res.Message = fmt.Sprintf("已放行 %d 个端口，%d 个原本已放行", len(res.Opened), len(res.AlreadyOpen))
	}
	return res, nil
}
