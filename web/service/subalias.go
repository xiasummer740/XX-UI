package service

import (
	"encoding/base64"
	"strings"
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
