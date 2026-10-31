package service

import (
	"encoding/base64"
	"strings"

	"github.com/XiaSummer740/XX-UI/database/model"
)

// 订阅 ID（subId）允许自定义，但它是订阅 URL 的路径段：
//   https://host:2096/sub/<subId>
// 一旦里面出现中文等非 ASCII 字符，URL 里就会出现 %XX 编码的字节，
// 部分客户端（典型是 Shadowrocket 小火箭）会直接报「错误的 URL」/「识别失败」，
// 尽管浏览器和服务端都能正常处理。
//
// 这里提供一层 ASCII 别名：本身是 URL 安全的纯 ASCII 时原样使用，
// 否则编码成 "b64-<base64url(utf8)>"，URL 全程 ASCII，所有客户端都能解析；
// 服务端收到 b64- 前缀的令牌会自动还原成真实订阅 ID 再查库。

// SubAliasPrefix 是 ASCII 别名的前缀。
const SubAliasPrefix = "b64-"

func isSubIDTokenSafe(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.', r == '~':
		default:
			return false
		}
	}
	return true
}

// EncodeSubIDToken 返回可以安全放进 URL 路径的订阅 ID 令牌。
// 已经是安全 ASCII 的原样返回，避免改变现有订阅链接。
func EncodeSubIDToken(subID string) string {
	if isSubIDTokenSafe(subID) {
		return subID
	}
	return SubAliasPrefix + base64.RawURLEncoding.EncodeToString([]byte(subID))
}

// DecodeSubIDToken 把 b64- 令牌还原为真实订阅 ID；不是令牌时原样返回。
func DecodeSubIDToken(token string) string {
	if !strings.HasPrefix(token, SubAliasPrefix) {
		return token
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, SubAliasPrefix))
	if err != nil || len(raw) == 0 {
		return token
	}
	return string(raw)
}

// SubIDCandidates 返回查库时应依次尝试的订阅 ID（原值优先，其次别名还原值）。
func SubIDCandidates(subID string) []string {
	out := []string{subID}
	if alt := DecodeSubIDToken(subID); alt != subID {
		out = append(out, alt)
	}
	return out
}

// IsSubIDURLSafe 报告订阅 ID 是否能直接用在 URL 路径里（供面板校验提示用）。
func IsSubIDURLSafe(subID string) bool {
	return isSubIDTokenSafe(subID)
}

// MaxPrevSubIDs 每个客户端保留的历史订阅 ID 上限。
const MaxPrevSubIDs = 20

// 订阅 ID 允许随时改，但它是订阅 URL 的路径段。为了让已经发出去的
// 老链接继续可用，每次改 ID 都把旧 ID 记进客户端的 prevSubIds，
// 查询时 —— 当前 ID + 历史 ID 都能命中同一个客户端。

// MergePrevSubIDs 把 oldSubID 合并进历史列表（去重、去掉空值、限制长度）。
func MergePrevSubIDs(prev []string, oldSubID string) []string {
	oldSubID = strings.TrimSpace(oldSubID)
	if oldSubID == "" {
		return prev
	}
	out := make([]string, 0, len(prev)+1)
	seen := make(map[string]bool, len(prev)+1)
	for _, p := range prev {
		p = strings.TrimSpace(p)
		if p == "" || p == oldSubID || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	out = append(out, oldSubID)
	if len(out) > MaxPrevSubIDs {
		out = out[len(out)-MaxPrevSubIDs:]
	}
	return out
}

// ClientMatchesSubID 判断某个订阅 ID（含历史 ID）是否命中该客户端。
func ClientMatchesSubID(c model.Client, subID string) bool {
	if c.SubID == subID {
		return true
	}
	for _, p := range c.PrevSubIDs {
		if p == subID {
			return true
		}
	}
	return false
}

// PrevSubIDsFromAny 把 settings JSON 里反序列化出来的任意形态转成 []string。
func PrevSubIDsFromAny(v any) []string {
	switch raw := v.(type) {
	case []any:
		out := make([]string, 0, len(raw))
		for _, it := range raw {
			if s, ok := it.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return raw
	}
	return nil
}

// SubIDsToAny 把字符串切片转成可以直接塞回 map[string]any 的形态。
func SubIDsToAny(list []string) []any {
	out := make([]any, 0, len(list))
	for _, s := range list {
		out = append(out, s)
	}
	return out
}

// RecordSubIDHistory 在保存客户端前调用：如果订阅 ID 变了，
// 把旧 ID 记进 prevSubIds，让已经发出去的订阅链接继续有效。
// 入参都是 settings JSON 里的客户端对象。
func RecordSubIDHistory(oldClient, newClient map[string]any) {
	if oldClient == nil || newClient == nil {
		return
	}
	prev := PrevSubIDsFromAny(newClient["prevSubIds"])
	prev = append(prev, PrevSubIDsFromAny(oldClient["prevSubIds"])...)
	oldSub, _ := oldClient["subId"].(string)
	newSub, _ := newClient["subId"].(string)
	merged := prev
	if oldSub != "" && oldSub != newSub {
		merged = MergePrevSubIDs(prev, oldSub)
	}
	if len(merged) == 0 {
		delete(newClient, "prevSubIds")
		return
	}
	newClient["prevSubIds"] = SubIDsToAny(merged)
}
