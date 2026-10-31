package entity

// Session 一次业务登录会话,按 token 哈希存储,多设备并存。
type Session struct {
	UserID    string
	AppID     string
	IssuedAt  int64
	ExpiresAt int64
}

// LoginResult 登录成功返回给调用方的凭据与玩家资料;Token 为明文,只返回一次。
type LoginResult struct {
	Token     string
	ExpiresAt int64
	Player    *Person
}
