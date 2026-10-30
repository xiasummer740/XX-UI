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
