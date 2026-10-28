package service

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
)

// fakeSocks5 is a minimal SOCKS5 server good enough to exercise the handshake:
// optional username/password authentication and a CONNECT that is always
// answered with the configured reply code (so no real egress is needed).
type fakeSocks5 struct {
	requireAuth bool
	wantUser    string
	wantPass    string
	connectCode byte
}

func (f *fakeSocks5) serve(t *testing.T, ln net.Listener) {
	t.Helper()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			head := make([]byte, 2)
			if _, err := io.ReadFull(c, head); err != nil {
				return
			}
			if head[0] != 0x05 {
				return
			}
			methods := make([]byte, int(head[1]))
			if _, err := io.ReadFull(c, methods); err != nil {
				return
			}
			if f.requireAuth {
				if _, err := c.Write([]byte{0x05, 0x02}); err != nil {
					return
				}
				ver := make([]byte, 2)
				if _, err := io.ReadFull(c, ver); err != nil {
					return
				}
				user := make([]byte, int(ver[1]))
				if _, err := io.ReadFull(c, user); err != nil {
					return
				}
				plen := make([]byte, 1)
				if _, err := io.ReadFull(c, plen); err != nil {
					return
				}
				pass := make([]byte, int(plen[0]))
				if _, err := io.ReadFull(c, pass); err != nil {
					return
				}
				if string(user) != f.wantUser || string(pass) != f.wantPass {
					_, _ = c.Write([]byte{0x01, 0x01})
					return
				}
				if _, err := c.Write([]byte{0x01, 0x00}); err != nil {
					return
				}
			} else {
				if _, err := c.Write([]byte{0x05, 0x00}); err != nil {
					return
				}
			}
			// CONNECT request: ver cmd rsv atyp addr port
			req := make([]byte, 4)
			if _, err := io.ReadFull(c, req); err != nil {
				return
			}
			switch req[3] {
			case 0x01:
				if _, err := io.ReadFull(c, make([]byte, 6)); err != nil {
					return
				}
			case 0x03:
				l := make([]byte, 1)
				if _, err := io.ReadFull(c, l); err != nil {
					return
				}
				if _, err := io.ReadFull(c, make([]byte, int(l[0])+2)); err != nil {
					return
				}
			default:
				return
			}
			_, _ = c.Write([]byte{0x05, f.connectCode, 0x00, 0x01, 127, 0, 0, 1, 0, 80})
		}(conn)
	}
}

// fakeHTTPProxy answers CONNECT with a fixed status code.
func (f *fakeHTTPProxy) serve(t *testing.T, ln net.Listener) {
	t.Helper()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			reader := bufio.NewReader(c)
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimSpace(line) == "" {
					break
				}
			}
			_, _ = fmt.Fprintf(c, "HTTP/1.1 %d %s\r\n\r\n", f.code, "test")
		}(conn)
	}
}

type fakeHTTPProxy struct {
	code int
}

func startSocks5(t *testing.T, f *fakeSocks5) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go f.serve(t, ln)
	return ln.Addr().(*net.TCPAddr).Port
}

func startHTTPProxy(t *testing.T, f *fakeHTTPProxy) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go f.serve(t, ln)
	return ln.Addr().(*net.TCPAddr).Port
}

func TestChainProxySocks5(t *testing.T) {
	svc := &InboundService{}

	t.Run("no auth", func(t *testing.T) {
		port := startSocks5(t, &fakeSocks5{})
		res := svc.TestChainProxy("socks", "127.0.0.1", port, "", "")
		if res["reachable"] != true || res["ok"] != true || res["targetOk"] != true {
			t.Fatalf("expected full success, got %v", res)
		}
		if res["auth"] != "none" {
			t.Fatalf("auth = %v, want none", res["auth"])
		}
	})

	t.Run("auth ok", func(t *testing.T) {
		port := startSocks5(t, &fakeSocks5{requireAuth: true, wantUser: "u", wantPass: "p"})
		res := svc.TestChainProxy("socks", "127.0.0.1", port, "u", "p")
		if res["ok"] != true || res["targetOk"] != true {
			t.Fatalf("expected handshake success, got %v", res)
		}
		if res["auth"] != "password" {
			t.Fatalf("auth = %v, want password", res["auth"])
		}
	})

	t.Run("auth wrong", func(t *testing.T) {
		port := startSocks5(t, &fakeSocks5{requireAuth: true, wantUser: "u", wantPass: "p"})
		res := svc.TestChainProxy("socks", "127.0.0.1", port, "u", "wrong")
		if res["reachable"] != true {
			t.Fatalf("port should still be reachable, got %v", res)
		}
		if res["ok"] != false {
			t.Fatalf("expected ok=false for bad credentials, got %v", res)
		}
		if msg, _ := res["error"].(string); !strings.Contains(msg, "认证失败") {
			t.Fatalf("error should mention auth failure, got %q", msg)
		}
	})

	t.Run("auth required but not provided", func(t *testing.T) {
		port := startSocks5(t, &fakeSocks5{requireAuth: true, wantUser: "u", wantPass: "p"})
		res := svc.TestChainProxy("socks", "127.0.0.1", port, "", "")
		if res["ok"] != false {
			t.Fatalf("expected ok=false, got %v", res)
		}
	})

	t.Run("proxy refuses target", func(t *testing.T) {
		port := startSocks5(t, &fakeSocks5{connectCode: 0x05})
		res := svc.TestChainProxy("socks", "127.0.0.1", port, "", "")
		if res["ok"] != true {
			t.Fatalf("handshake should succeed, got %v", res)
		}
		if res["targetOk"] != false {
			t.Fatalf("targetOk should be false, got %v", res)
		}
		if msg, _ := res["error"].(string); !strings.Contains(msg, "拒绝") {
			t.Fatalf("error should explain the refusal, got %q", msg)
		}
	})
}

func TestChainProxyHTTP(t *testing.T) {
	svc := &InboundService{}

	t.Run("connect 200", func(t *testing.T) {
		port := startHTTPProxy(t, &fakeHTTPProxy{code: 200})
		res := svc.TestChainProxy("http", "127.0.0.1", port, "", "")
		if res["ok"] != true || res["targetOk"] != true {
			t.Fatalf("expected success, got %v", res)
		}
	})

	t.Run("connect 407", func(t *testing.T) {
		port := startHTTPProxy(t, &fakeHTTPProxy{code: 407})
		res := svc.TestChainProxy("http", "127.0.0.1", port, "u", "p")
		if res["ok"] != false {
			t.Fatalf("expected ok=false, got %v", res)
		}
		if msg, _ := res["error"].(string); !strings.Contains(msg, "407") {
			t.Fatalf("error should mention 407, got %q", msg)
		}
	})
}

func TestChainProxyUnreachablePort(t *testing.T) {
	svc := &InboundService{}
	// Reserve then release a port so nothing is listening there.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	res := svc.TestChainProxy("socks", "127.0.0.1", port, "", "")
	if res["reachable"] != false {
		t.Fatalf("expected reachable=false, got %v", res)
	}
	if msg, _ := res["error"].(string); !strings.Contains(msg, "端口不可达") {
		t.Fatalf("error should mention unreachable port, got %q", msg)
	}
}

func TestChainProxyRejectsLinkLocal(t *testing.T) {
	svc := &InboundService{}
	res := svc.TestChainProxy("socks", "169.254.169.254", 80, "", "")
	if res["reachable"] != false {
		t.Fatalf("metadata address must be rejected, got %v", res)
	}
}

// Sanity check that the SOCKS5 status byte mapping covers the codes we report.
func TestSocksReplyMessage(t *testing.T) {
	if socksReplyMessage(0x05) == "" {
		t.Fatal("expected a message for 0x05")
	}
	if !strings.Contains(socksReplyMessage(0x05), "拒绝") {
		t.Fatalf("unexpected message: %s", socksReplyMessage(0x05))
	}
}
