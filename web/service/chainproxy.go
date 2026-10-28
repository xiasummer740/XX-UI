package service

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// chainProxyTestTarget is what the proxy is asked to CONNECT to. It is a fixed
// IP:port on purpose: no DNS resolution is needed at the proxy side, so a
// failure here is about the proxy's own egress rather than about DNS.
const chainProxyTestTarget = "1.1.1.1:443"

// chainProxyDialTimeout bounds the TCP connect; the rest of the handshake is
// bounded by a deadline on the connection.
const chainProxyDialTimeout = 6 * time.Second

// chainProxyHandshakeTimeout bounds handshake + CONNECT.
const chainProxyHandshakeTimeout = 8 * time.Second

// TestChainProxy verifies a chain proxy at the protocol level: TCP connect,
// SOCKS5 / HTTP CONNECT handshake including authentication, and a CONNECT to a
// public host.
//
// A plain TCP port check (CheckRemotePort) answers "is something listening",
// which is not the question that matters here: a wrong password, an HTTP proxy
// on a SOCKS port, or a proxy that cannot reach the internet all pass a port
// check while every packet of real traffic fails. The returned map separates
// those cases:
//
//	reachable  TCP connect succeeded
//	ok         handshake (and authentication) succeeded
//	targetOk   the proxy accepted the CONNECT (its own egress works)
func (s *InboundService) TestChainProxy(protocol, address string, port int, user, password string) map[string]any {
	result := map[string]any{
		"host":      address,
		"port":      port,
		"protocol":  protocol,
		"target":    chainProxyTestTarget,
		"reachable": false,
		"ok":        false,
		"targetOk":  false,
		"latency":   0,
	}
	if address == "" || port <= 0 || port > 65535 {
		result["error"] = "代理地址或端口无效"
		return result
	}
	// Metadata endpoints are never a legitimate chain proxy target.
	if ip := net.ParseIP(address); ip != nil && ip.IsLinkLocalUnicast() {
		result["error"] = "link-local/metadata addresses are not allowed"
		return result
	}

	start := time.Now()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(address, strconv.Itoa(port)), chainProxyDialTimeout)
	result["latency"] = time.Since(start).Milliseconds()
	if err != nil {
		result["error"] = "端口不可达: " + err.Error()
		return result
	}
	defer conn.Close()
	result["reachable"] = true

	_ = conn.SetDeadline(time.Now().Add(chainProxyHandshakeTimeout))

	proto := strings.ToLower(strings.TrimSpace(protocol))
	if proto == "" {
		proto = "socks"
	}
	switch proto {
	case "socks", "socks5":
		result["protocol"] = "socks"
		s.testSocks5Chain(conn, result, user, password)
	case "http", "https":
		result["protocol"] = "http"
		s.testHTTPProxyChain(conn, result, user, password)
	default:
		// Unknown protocol: fall back to the plain TCP result, which is all we
		// can honestly claim.
		result["warning"] = "不支持的代理协议，仅验证了端口连通性"
		return result
	}
	return result
}

// testSocks5Chain performs the RFC 1928 handshake plus RFC 1929 authentication
// and a CONNECT request, mutating result with what it learned.
func (s *InboundService) testSocks5Chain(conn net.Conn, result map[string]any, user, password string) {
	methods := []byte{0x00} // no authentication
	if user != "" {
		methods = []byte{0x00, 0x02} // no-auth or username/password
	}
	greeting := append([]byte{0x05, byte(len(methods))}, methods...)
	if _, err := conn.Write(greeting); err != nil {
		result["error"] = "握手失败: " + err.Error()
		return
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		result["error"] = "握手失败（对端不是 SOCKS5 代理？）: " + err.Error()
		return
	}
	if reply[0] != 0x05 {
		result["error"] = fmt.Sprintf("对端不是 SOCKS5 代理（版本字节 0x%02x）", reply[0])
		return
	}

	switch reply[1] {
	case 0x00:
		result["auth"] = "none"
	case 0x02:
		if user == "" {
			result["error"] = "代理要求用户名密码，请填写后再测试"
			return
		}
		if len(user) > 255 || len(password) > 255 {
			result["error"] = "用户名或密码过长（上限 255 字节）"
			return
		}
		authReq := make([]byte, 0, 3+len(user)+len(password))
		authReq = append(authReq, 0x01, byte(len(user)))
		authReq = append(authReq, user...)
		authReq = append(authReq, byte(len(password)))
		authReq = append(authReq, password...)
		if _, err := conn.Write(authReq); err != nil {
			result["error"] = "认证请求发送失败: " + err.Error()
			return
		}
		authReply := make([]byte, 2)
		if _, err := io.ReadFull(conn, authReply); err != nil {
			result["error"] = "认证应答读取失败: " + err.Error()
			return
		}
		if authReply[1] != 0x00 {
			result["error"] = "认证失败：用户名或密码错误"
			return
		}
		result["auth"] = "password"
	case 0xFF:
		result["error"] = "代理拒绝了所有认证方式（可能只接受用户名密码，或 IP 未授权）"
		return
	default:
		result["error"] = fmt.Sprintf("代理要求不支持的认证方式 0x%02x", reply[1])
		return
	}
	result["ok"] = true

	// CONNECT 1.1.1.1:443 through the proxy.
	host, portStr, err := net.SplitHostPort(chainProxyTestTarget)
	if err != nil {
		result["error"] = "目标解析失败: " + err.Error()
		return
	}
	ip := net.ParseIP(host).To4()
	if ip == nil {
		result["error"] = "测试目标必须是 IPv4 字面量"
		return
	}
	portNum, err := strconv.Atoi(portStr)
	if err != nil {
		result["error"] = "测试目标端口无效"
		return
	}
	req := []byte{0x05, 0x01, 0x00, 0x01, ip[0], ip[1], ip[2], ip[3], byte(portNum >> 8), byte(portNum)}
	if _, err := conn.Write(req); err != nil {
		result["error"] = "CONNECT 请求发送失败: " + err.Error()
		return
	}
	resp := make([]byte, 4)
	if _, err := io.ReadFull(conn, resp); err != nil {
		result["error"] = "CONNECT 应答读取失败: " + err.Error()
		return
	}
	if resp[1] != 0x00 {
		result["error"] = "代理拒绝连接目标: " + socksReplyMessage(resp[1])
		return
	}
	result["targetOk"] = true
}

// testHTTPProxyChain performs a CONNECT request through an HTTP proxy, with
// optional Basic authentication.
func (s *InboundService) testHTTPProxyChain(conn net.Conn, result map[string]any, user, password string) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n", chainProxyTestTarget, chainProxyTestTarget)
	if user != "" {
		cred := base64.StdEncoding.EncodeToString([]byte(user + ":" + password))
		fmt.Fprintf(&sb, "Proxy-Authorization: Basic %s\r\n", cred)
	}
	sb.WriteString("Proxy-Connection: keep-alive\r\n\r\n")
	if _, err := conn.Write([]byte(sb.String())); err != nil {
		result["error"] = "CONNECT 请求发送失败: " + err.Error()
		return
	}

	reader := bufio.NewReader(conn)
	statusLine, err := reader.ReadString('\n')
	if err != nil {
		result["error"] = "代理无响应（对端不是 HTTP 代理？）: " + err.Error()
		return
	}
	fields := strings.Fields(strings.TrimSpace(statusLine))
	if len(fields) < 2 {
		result["error"] = "代理返回了无法解析的响应: " + strings.TrimSpace(statusLine)
		return
	}
	code, err := strconv.Atoi(fields[1])
	if err != nil {
		result["error"] = "代理返回了无法解析的状态码: " + fields[1]
		return
	}
	switch {
	case code == 200:
		result["auth"] = "none"
		if user != "" {
			result["auth"] = "password"
		}
		result["ok"] = true
		result["targetOk"] = true
		// Drain the remaining header lines so the connection is left clean.
		for {
			line, err := reader.ReadString('\n')
			if err != nil || strings.TrimSpace(line) == "" {
				break
			}
		}
	case code == 407:
		if user == "" {
			result["error"] = "代理要求认证（407），请填写用户名密码后再测试"
		} else {
			result["error"] = "认证失败（407）：用户名或密码错误"
		}
	default:
		result["error"] = fmt.Sprintf("代理拒绝了 CONNECT（HTTP %d）", code)
	}
}

// socksReplyMessage maps an RFC 1928 reply code to a readable reason.
func socksReplyMessage(rep byte) string {
	switch rep {
	case 0x01:
		return "代理内部错误"
	case 0x02:
		return "规则不允许该连接"
	case 0x03:
		return "网络不可达"
	case 0x04:
		return "目标主机不可达"
	case 0x05:
		return "目标端口拒绝连接"
	case 0x06:
		return "TTL 过期"
	case 0x07:
		return "不支持的命令"
	case 0x08:
		return "不支持的地址类型"
	default:
		return fmt.Sprintf("未知错误码 0x%02x", rep)
	}
}
