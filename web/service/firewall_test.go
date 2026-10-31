package service

import "testing"

func TestUFWIsActive(t *testing.T) {
	active := `Status: active
Logging: on (low)
Default: deny (incoming), allow (outgoing), deny (routed)
`
	if !ufwIsActive(active) {
		t.Error("应识别为已启用")
	}
	inactive := "Status: inactive\n"
	if ufwIsActive(inactive) {
		t.Error("应识别为未启用")
	}
}

func TestUFWPortAllowed(t *testing.T) {
	status := `Status: active
To                         Action      From
--                         ------      ----
22/tcp                     ALLOW IN    Anywhere
443/tcp                    ALLOW IN    Anywhere
8443/tcp                   ALLOW       Anywhere                   # inbound 8443 REALITY
8443/tcp (v6)              ALLOW       Anywhere (v6)              # inbound 8443 REALITY
20443/tcp                  ALLOW IN    Anywhere
`

	cases := []struct {
		port  int
		proto string
		want  bool
	}{
		{443, "tcp", true},
		{8443, "tcp", true}, // 带注释也要认出来
		{22, "tcp", true},
		{443, "udp", false},  // 只放行了 tcp
		{8443, "udp", false}, // 只放行了 tcp
		{4430, "tcp", false}, // 不能被子串 443 误判
		{4433, "tcp", false},
		{20443, "tcp", true},
		{10000, "tcp", false},
	}
	for _, c := range cases {
		if got := ufwPortAllowed(status, c.port, c.proto); got != c.want {
			t.Errorf("port=%d/%s 期望 %v，实际 %v", c.port, c.proto, c.want, got)
		}
	}
}

// VPS 上真实 `ufw status` 的片段：很多规则是 `ufw allow <端口>` 加的，
// 不带 /tcp，之前会被误判成「未放行」。
const ufwBarePortSample = `Status: active
To                         Action      From
--                         ------      ----
[ 3] 443                        ALLOW IN    Anywhere                  
[10] 39760                      ALLOW IN    Anywhere                  
[11] 14704/tcp                  ALLOW IN    Anywhere                   # DM US VLESS Reality
[13] 42720                      ALLOW IN    Anywhere                  
[14] 37087                      ALLOW IN    Anywhere                  
[17] 21666/tcp                  ALLOW IN    Anywhere                  
[21] 8443/tcp                   ALLOW IN    Anywhere                   # inbound 8443 REALITY
[27] 443 (v6)                   ALLOW IN    Anywhere (v6)             
[99] 2087/tcp                   ALLOW IN    69.63.205.35              
`

func TestUFWPortAllowedBarePort(t *testing.T) {
	cases := []struct {
		port  int
		proto string
		want  bool
		why   string
	}{
		{443, "tcp", true, "443 不带协议（ufw allow 443）"},
		{39760, "tcp", true, "39760 不带协议"},
		{37087, "tcp", true, "37087 不带协议"},
		{42720, "tcp", true, "42720 不带协议"},
		{14704, "tcp", true, "14704/tcp 带注释"},
		{21666, "tcp", true, "21666/tcp"},
		{8443, "tcp", true, "8443/tcp"},
		{2087, "tcp", true, "只对某个源 IP 放行也算放行"},
		{2053, "tcp", false, "确实没放行的端口要报出来"},
		{4430, "tcp", false, "不能被子串误判"},
	}
	for _, c := range cases {
		if got := ufwPortAllowed(ufwBarePortSample, c.port, c.proto); got != c.want {
			t.Errorf("端口 %d/%s = %v，期望 %v（%s）", c.port, c.proto, got, c.want, c.why)
		}
	}
}

func TestUDPNeededForProtocol(t *testing.T) {
	for _, p := range []string{"hysteria", "hysteria2", "tuic", "wireguard"} {
		if !udpNeededForProtocol(p) {
			t.Errorf("%s 应需要 UDP", p)
		}
	}
	for _, p := range []string{"vless", "vmess", "trojan", "shadowsocks"} {
		if udpNeededForProtocol(p) {
			t.Errorf("%s 不需要额外放行 UDP", p)
		}
	}
}

func TestListensPublicly(t *testing.T) {
	for _, l := range []string{"", "0.0.0.0", "::", "::0", "  "} {
		if !listensPublicly(l) {
			t.Errorf("%q 应视为对外监听", l)
		}
	}
	for _, l := range []string{"127.0.0.1", "192.168.1.10", "::1"} {
		if listensPublicly(l) {
			t.Errorf("%q 不应视为对外监听", l)
		}
	}
}
