package service

import (
	"testing"

	"github.com/XiaSummer740/XX-UI/database/model"
)

func TestEncodeSubIDToken(t *testing.T) {
	cases := []struct{ in, want string }{
		{"8ufvbiwqk5zgufx2", "8ufvbiwqk5zgufx2"}, // 纯 ASCII 原样
		{"meiguo-kanjian", "meiguo-kanjian"},
		{"a.b_c~d", "a.b_c~d"},
		{"美国将看见了", SubAliasPrefix + "576O5Zu95bCG55yL6KeB5LqG"},
		{"十分士大夫", SubAliasPrefix + "5Y2B5YiG5aOr5aSn5aSr"},
		{"has space", SubAliasPrefix + "aGFzIHNwYWNl"},
		{"a/b", SubAliasPrefix + "YS9i"},
	}
	for _, c := range cases {
		if got := EncodeSubIDToken(c.in); got != c.want {
			t.Errorf("EncodeSubIDToken(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDecodeSubIDTokenRoundTrip(t *testing.T) {
	for _, s := range []string{"美国将看见了", "十分士大夫", "a b", "a/b#c", "中文 mixed 123"} {
		token := EncodeSubIDToken(s)
		if got := DecodeSubIDToken(token); got != s {
			t.Errorf("往返失败: %q -> %q -> %q", s, token, got)
		}
	}
	// 非令牌原样返回
	for _, s := range []string{"8ufvbiwqk5zgufx2", "b64-!!!notbase64", "b64-"} {
		if got := DecodeSubIDToken(s); got != s {
			t.Errorf("DecodeSubIDToken(%q) = %q, 应原样返回", s, got)
		}
	}
}

func TestSubIDCandidates(t *testing.T) {
	// 中文 ID：先试原值，再试还原值
	got := SubIDCandidates(EncodeSubIDToken("十分士大夫"))
	if len(got) != 2 || got[0] != "b64-5Y2B5YiG5aOr5aSn5aSr" || got[1] != "十分士大夫" {
		t.Errorf("候选列表不对: %#v", got)
	}
	// 安全 ASCII：只有一个候选
	if got := SubIDCandidates("abc123"); len(got) != 1 || got[0] != "abc123" {
		t.Errorf("ASCII 应只有一个候选: %#v", got)
	}
}

func TestIsSubIDURLSafe(t *testing.T) {
	if !IsSubIDURLSafe("meiguo-kanjian") {
		t.Error("ASCII 应判定为安全")
	}
	for _, s := range []string{"美国", "a b", "a/b", ""} {
		if IsSubIDURLSafe(s) {
			t.Errorf("%q 不应判定为安全", s)
		}
	}
}

// 改订阅 ID 之后，旧 ID 必须仍然命中同一个客户端。
func TestMergePrevSubIDs(t *testing.T) {
	got := MergePrevSubIDs(nil, "old-one")
	if len(got) != 1 || got[0] != "old-one" {
		t.Fatalf("MergePrevSubIDs(nil, old-one) = %v", got)
	}

	// 重复追加要去重，且空值被忽略
	got = MergePrevSubIDs([]string{"a", "", "old-one"}, "old-one")
	if len(got) != 2 || got[0] != "a" || got[1] != "old-one" {
		t.Fatalf("MergePrevSubIDs 去重失败: %v", got)
	}

	// 空旧值不应写入
	if got := MergePrevSubIDs([]string{"a"}, "  "); len(got) != 1 || got[0] != "a" {
		t.Fatalf("空旧订阅 ID 不应写入: %v", got)
	}

	// 超过上限时保留最近的
	var many []string
	for i := 0; i < MaxPrevSubIDs+5; i++ {
		many = append(many, string(rune('a'+i%26))+string(rune('0'+i%10)))
	}
	many = MergePrevSubIDs(many, "last-one")
	if len(many) != MaxPrevSubIDs || many[len(many)-1] != "last-one" {
		t.Fatalf("历史长度应被截断到 %d，实际 %d", MaxPrevSubIDs, len(many))
	}
}

func TestClientMatchesSubID(t *testing.T) {
	c := model.Client{SubID: "new-id", PrevSubIDs: []string{"old-id", "older-id"}}
	if !ClientMatchesSubID(c, "new-id") {
		t.Fatal("当前 ID 应命中")
	}
	if !ClientMatchesSubID(c, "old-id") {
		t.Fatal("历史 ID 应命中")
	}
	if ClientMatchesSubID(c, "someone-else") {
		t.Fatal("陌生 ID 不应命中")
	}
}
