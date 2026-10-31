package vo

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MaxNicknameRunes     = 64
	MaxAvatarURLBytes    = 2048
	avatarRequiredScheme = "https"
)

// Profile 玩家主动授权后提交的展示资料。它不是微信资料真实性的证明,
// 修改它不会改变玩家 ID 或权限。
type Profile struct {
	Nickname  string
	AvatarURL string
}

// Validate 昵称 1~64 个 Unicode 码点且不含控制字符;头像为空或合法 HTTPS URL。
func (p Profile) Validate() error {
	if err := validateNickname(p.Nickname); err != nil {
		return err
	}
	return validateAvatarURL(p.AvatarURL)
}

func validateNickname(name string) error {
	if !utf8.ValidString(name) {
		return errors.New("nickname is not valid UTF-8")
	}
	if strings.TrimSpace(name) == "" {
		return errors.New("nickname is required")
	}
	if utf8.RuneCountInString(name) > MaxNicknameRunes {
		return fmt.Errorf("nickname exceeds %d characters", MaxNicknameRunes)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return errors.New("nickname contains control characters")
		}
	}
	return nil
}

func validateAvatarURL(raw string) error {
	if raw == "" {
		return nil
	}
	if len(raw) > MaxAvatarURLBytes {
		return fmt.Errorf("avatar_url exceeds %d bytes", MaxAvatarURLBytes)
	}
	for _, r := range raw {
		if r <= ' ' || r == 0x7f {
			return errors.New("avatar_url contains whitespace or control characters")
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("avatar_url is not a valid URL")
	}
	if u.Scheme != avatarRequiredScheme {
		return errors.New("avatar_url must use https")
	}
	if u.User != nil {
		return errors.New("avatar_url must not contain credentials")
	}
	host := u.Hostname()
	if host == "" || !validHostname(host) {
		return errors.New("avatar_url has an invalid host")
	}
	return nil
}

func validHostname(host string) bool {
	if len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		for _, r := range label {
			if !(r == '-' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
				return false
			}
		}
	}
	return true
}
