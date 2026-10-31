package vo

import (
	"strings"
	"testing"
)

func TestProfileValidate(t *testing.T) {
	tests := []struct {
		name    string
		profile Profile
		ok      bool
	}{
		{"chinese nickname, no avatar", Profile{Nickname: "阿甘"}, true},
		{"emoji at limit", Profile{Nickname: strings.Repeat("🎴", 64)}, true},
		{"pipe allowed", Profile{Nickname: "a|b"}, true},
		{"wechat avatar", Profile{Nickname: "x", AvatarURL: "https://thirdwx.qlogo.cn/mmopen/vi_32/abc/132"}, true},
		{"empty nickname", Profile{Nickname: ""}, false},
		{"blank nickname", Profile{Nickname: "   "}, false},
		{"too long", Profile{Nickname: strings.Repeat("名", 65)}, false},
		{"control char", Profile{Nickname: "a\nb"}, false},
		{"invalid utf8", Profile{Nickname: "a\xffb"}, false},
		{"http avatar", Profile{Nickname: "x", AvatarURL: "http://thirdwx.qlogo.cn/a"}, false},
		{"credentials in url", Profile{Nickname: "x", AvatarURL: "https://user:pw@thirdwx.qlogo.cn/a"}, false},
		{"no host", Profile{Nickname: "x", AvatarURL: "https:///a.png"}, false},
		{"bad host", Profile{Nickname: "x", AvatarURL: "https://exa_mple.com/a"}, false},
		{"whitespace", Profile{Nickname: "x", AvatarURL: "https://a.com/a b"}, false},
		{"relative", Profile{Nickname: "x", AvatarURL: "/a.png"}, false},
		{"too long url", Profile{Nickname: "x", AvatarURL: "https://a.com/" + strings.Repeat("a", 2048)}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.profile.Validate()
			if (err == nil) != tt.ok {
				t.Errorf("Validate() = %v, want ok=%v", err, tt.ok)
			}
		})
	}
}
