package service

import (
	"os"
	"strconv"
	"strings"
)

// ConnFamilyStat 表示某个监听端口上已建立连接所使用的 IP 协议族数量。
type ConnFamilyStat struct {
	V4 int `json:"v4"`
	V6 int `json:"v6"`
}

const (
	procNetTCP  = "/proc/net/tcp"
	procNetTCP6 = "/proc/net/tcp6"

	// /proc/net/tcp{,6} 中 st 列的 01 表示 ESTABLISHED，0A 是 LISTEN。
	tcpStateEstablished = "01"

	// v4MappedPrefix 是 ::ffff:a.b.c.d 在 /proc/net/tcp6 里的前缀（前 10 字节为零 + 0xFFFF）。
	v4MappedPrefix = "0000000000000000FFFF"
)

// countEstablishedByPort 解析 /proc/net/tcp 与 /proc/net/tcp6 的内容，
// 按本地端口统计 ESTABLISHED 连接的协议族。
//
// 双栈监听（::）时 IPv4 客户端会以 ::ffff:a.b.c.d 的映射地址出现在 tcp6 里，
// 这类连接必须计入 IPv4，否则 IPv4 连接会被误判成 IPv6。
func countEstablishedByPort(tcp4, tcp6 []byte) map[int]*ConnFamilyStat {
	stats := make(map[int]*ConnFamilyStat)
	collect := func(data []byte, fromV6File bool) {
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			// sl local_address rem_address st ...
			if len(fields) < 4 || fields[0] == "sl" {
				continue
			}
			if fields[3] != tcpStateEstablished {
				continue
			}
			port, v6, ok := parseProcLocalAddr(fields[1], fromV6File)
			if !ok {
				continue
			}
			st := stats[port]
			if st == nil {
				st = &ConnFamilyStat{}
				stats[port] = st
			}
			if v6 {
				st.V6++
			} else {
				st.V4++
			}
		}
	}
	collect(tcp4, false)
	collect(tcp6, true)
	return stats
}

// parseProcLocalAddr 解析 "0100007F:1F90" 形式的本地地址，返回端口与是否为真实 IPv6。
func parseProcLocalAddr(field string, fromV6File bool) (int, bool, bool) {
	idx := strings.LastIndex(field, ":")
	if idx <= 0 || idx == len(field)-1 {
		return 0, false, false
	}
	port64, err := strconv.ParseInt(field[idx+1:], 16, 32)
	if err != nil || port64 <= 0 {
		return 0, false, false
	}
	addr := field[:idx]
	if !fromV6File {
		return int(port64), false, true
	}
	// tcp6 里出现的 v4-mapped 地址按 IPv4 计
	if strings.HasPrefix(strings.ToUpper(addr), v4MappedPrefix) {
		return int(port64), false, true
	}
	return int(port64), true, true
}

// establishedConnFamilies 读取当前内核的连接表，按本地端口统计协议族。
// 读取失败（非 Linux 或权限不足）时返回空表，调用方按“没有连接”处理。
func (s *InboundService) establishedConnFamilies() map[int]*ConnFamilyStat {
	tcp4, err4 := os.ReadFile(procNetTCP)
	tcp6, err6 := os.ReadFile(procNetTCP6)
	if err4 != nil && err6 != nil {
		return map[int]*ConnFamilyStat{}
	}
	return countEstablishedByPort(tcp4, tcp6)
}
