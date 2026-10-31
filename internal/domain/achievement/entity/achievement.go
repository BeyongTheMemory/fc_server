package entity

// Achievement 玩家解锁的一项成就。同一玩家的同一成就只保留首次解锁。
type Achievement struct {
	UserID        string
	AchievementID string
	UnlockedAtMs  int64  // 解锁时间(客户端上报,缺省为服务端接收时间),Unix 毫秒
	ReceivedAtMs  int64  // 服务端首次接收时间,Unix 毫秒
	Detail        []byte // 可选的附加信息,紧凑 JSON 对象;nil 表示没有
	ClientIP      string // 首次上报请求的来源 IP
}
