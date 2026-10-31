package entity

// Run 一张对局票:玩家开一局可上榜的对局时由服务端签发(种子也由服务端生成),
// 结束上报成绩时凭票提交,每局只能提交一次成绩。梦蝶局(玩家自定义种子)不领票、不上报成绩。
type Run struct {
	ID          string // 32 位十六进制随机串
	UserID      string
	Seed        string // 本局随机种子
	StartedAtMs int64
	ExpiresAtMs int64
}
