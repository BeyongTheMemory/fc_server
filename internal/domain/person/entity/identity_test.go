package entity

import (
	"strings"
	"testing"

	"fc_server/internal/domain/person/entity/vo"
)

// 固定测试向量:算法(v1、\0 分隔、base64url 无填充)属于对外契约,变更即破坏玩家身份。
func TestPlayerIDFixedVector(t *testing.T) {
	const want = "wx_pXPJm6VxJyqTEh380eIxSDK0XOsmHwpS-waglkJKiw4"
	got := PlayerID("wx1234567890abcdef", "oABCD-openid_example")
	if got != want {
		t.Fatalf("PlayerID = %q, want %q", got, want)
	}
}

func TestPlayerIDShapeAndSeparation(t *testing.T) {
	id := PlayerID("app", "openid")
	if len(id) != 46 || !strings.HasPrefix(id, PlayerIDPrefix) {
		t.Errorf("id = %q (len %d), want wx_ + 43 chars", id, len(id))
	}
	if strings.ContainsAny(id, "|+/= \t") {
		t.Errorf("id contains characters unsafe for rank keys: %q", id)
	}
	if PlayerID("app", "openid") != id {
		t.Error("PlayerID must be deterministic")
	}
	if PlayerID("ap", "popenid") == id || PlayerID("other", "openid") == id {
		t.Error("separator/appid must distinguish identities")
	}
}

func TestHashTokenIsHex(t *testing.T) {
	h := HashToken("token")
	if len(h) != 64 || h == "token" {
		t.Errorf("HashToken = %q", h)
	}
}

func TestDisplayNameFallback(t *testing.T) {
	if (&Person{}).DisplayName() != DefaultNickname {
		t.Error("empty nickname should fall back to default")
	}
	if (&Person{Profile: vo.Profile{Nickname: "阿甘"}}).DisplayName() != "阿甘" {
		t.Error("nickname should be used when set")
	}
}
