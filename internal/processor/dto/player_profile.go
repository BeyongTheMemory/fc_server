package dto

import (
	"fc_server/internal/domain/person/entity"
	"fc_server/internal/domain/person/entity/vo"
)

// UserDto 对外的玩家资料;不包含 openid、appid 等微信内部标识。
// nickname 未授权时为“无名”,avatar_url 未授权时为空(客户端显示默认头像)。
type UserDto struct {
	UserId    string `json:"user_id"`
	Nickname  string `json:"nickname"`
	AvatarUrl string `json:"avatar_url"`
}

func NewUserDto(p *entity.Person) *UserDto {
	return &UserDto{
		UserId:    p.ID,
		Nickname:  p.DisplayName(),
		AvatarUrl: p.Profile.AvatarURL,
	}
}

// ProfileDto 玩家授权后的微信头像昵称;用于 PATCH /users/me/profile 与 POST /upload_score 的 profile。
// 只更新当前 token 对应的玩家,两个字段整体替换。
//
// 客户端要求:
//   - 首次获取必须由玩家点击原生 wx.createUserInfoButton 触发(withCredentials: false),
//     不能用 wx.authorize({scope: 'scope.userInfo'}) 代替;已授权后可用 wx.getUserInfo 静默刷新。
//   - nickname = userInfo.nickName,avatar_url = userInfo.avatarUrl,原样提交,不要自行拼接或改写。
//   - 拒绝、取消授权或接口失败时不要提交 profile,也不要阻塞单机游玩;
//     业务 token 过期不代表头像授权被撤销,两者分开处理。
//   - 小游戏后台的隐私保护指引须声明收集“用户信息(昵称、头像)”,用途为排行榜展示。
//   - 校验规则:nickname 1~64 个字符、不含控制字符;avatar_url 可为空,
//     否则须为 https 链接且 ≤2048 字节。不合法返回 400。
type ProfileDto struct {
	Nickname  string `json:"nickname"`
	AvatarUrl string `json:"avatar_url"`
}

func (r *ProfileDto) ToProfile() vo.Profile {
	return vo.Profile{Nickname: r.Nickname, AvatarURL: r.AvatarUrl}
}
