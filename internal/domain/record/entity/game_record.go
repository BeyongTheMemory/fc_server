package entity

// GameRecord 玩家上报的一条游戏记录(日志型,只追加不修改)。
// Data 是客户端提交的 JSON 对象原文(已规整为紧凑格式),服务端不解析其业务含义。
type GameRecord struct {
	ID          string // 服务端生成的 32 位十六进制随机串,用于持久化去重
	UserID      string
	Data        []byte
	CreatedAtMs int64  // 服务端接收时间,Unix 毫秒
	ClientIP    string // 上报请求的来源 IP
}
