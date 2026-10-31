package entity

import (
	"fc_server/internal/domain/person/entity/vo"
)

// DefaultNickname 玩家未授权资料时对外展示的昵称。
const DefaultNickname = "无名"

// Person 玩家。ID 由 (AppID, OpenID) 确定性生成,见 PlayerID。
type Person struct {
	ID            string
	AppID         string
	OpenID        string
	Profile       vo.Profile
	CreatedTime   int64
	LastLoginTime int64
	UpdatedTime   int64
	// Revision 每次登录或资料变化时递增,用于 MySQL 备份只接受更新版本。
	Revision        int64
	OngoingGameMeta *vo.GameMeta
}

// DisplayName 资料中的昵称,未授权时回退为默认昵称。
func (p *Person) DisplayName() string {
	if p.Profile.Nickname == "" {
		return DefaultNickname
	}
	return p.Profile.Nickname
}
