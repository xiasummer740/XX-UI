package service

import "testing"

// procLine 拼一行 /proc/net/tcp{,6} 记录，字段顺序与内核输出一致。
func procLine(localAddr, state string) string {
	return "   0: " + localAddr + " 0100007F:9C40 " + state +
		" 00000000:00000000 00:00000000 00000000     0        0 12345 1 0000000000000000 20 4 30 10 -1\n"
}

func TestCountEstablishedByPort(t *testing.T) {
	const portHex = "9B50" // 39760
	// ::ffff:69.63.205.35 在 tcp6 中的形式
	const mapped = "0000000000000000FFFF23CD3F45"
	// 真实的 IPv6 地址（2409:8a55:392d:9f60::2 之类，非 v4-mapped）
	const realV6 = "B80D0120000000000000000000000000"

	tcp4 := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n" +
		procLine("0100007F:"+portHex, "01") + // 127.0.0.1:39760 已建立 -> v4
		procLine("0100007F:1F90", "0A") + // 8080 只有监听，不统计
		procLine("0100007F:"+portHex, "0A") // 39760 的监听套接字，忽略

	tcp6 := "  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n" +
		procLine(mapped+":"+portHex, "01") + // v4-mapped -> 计为 v4
		procLine(realV6+":"+portHex, "01") // 真实 v6 -> v6

	stats := countEstablishedByPort([]byte(tcp4), []byte(tcp6))

	st := stats[39760]
	if st == nil {
		t.Fatalf("端口 39760 没有统计结果")
	}
	if st.V4 != 2 {
		t.Errorf("V4 = %d, 期望 2（tcp4 一条 + v4-mapped 一条）", st.V4)
	}
	if st.V6 != 1 {
		t.Errorf("V6 = %d, 期望 1", st.V6)
	}
	if _, ok := stats[8080]; ok {
		t.Errorf("不应统计没有 ESTABLISHED 连接的端口 8080")
	}
}

func TestCountEstablishedByPortEmpty(t *testing.T) {
	if got := countEstablishedByPort(nil, nil); len(got) != 0 {
		t.Errorf("空输入应返回空表，实际 %v", got)
	}
	// 只有表头
	header := []byte("  sl  local_address rem_address   st\n")
	if got := countEstablishedByPort(header, header); len(got) != 0 {
		t.Errorf("只有表头时应返回空表，实际 %v", got)
	}
}

func TestParseProcLocalAddr(t *testing.T) {
	cases := []struct {
		field   string
		fromV6  bool
		wantP   int
		wantV6  bool
		wantOK  bool
		comment string
	}{
		{"0100007F:1F90", false, 8080, false, true, "IPv4 本地地址"},
		{"0000000000000000FFFF23CD3F45:9B50", true, 39760, false, true, "v4-mapped 计为 IPv4"},
		{"B80D0120000000000000000000000000:9B50", true, 39760, true, true, "真实 IPv6"},
		{"", false, 0, false, false, "空字段"},
		{"0100007F", false, 0, false, false, "缺少端口"},
		{"0100007F:", false, 0, false, false, "端口为空"},
	}
	for _, c := range cases {
		p, v6, ok := parseProcLocalAddr(c.field, c.fromV6)
		if ok != c.wantOK || p != c.wantP || v6 != c.wantV6 {
			t.Errorf("%s: 得到 (port=%d, v6=%v, ok=%v)，期望 (port=%d, v6=%v, ok=%v)",
				c.comment, p, v6, ok, c.wantP, c.wantV6, c.wantOK)
		}
	}
}
