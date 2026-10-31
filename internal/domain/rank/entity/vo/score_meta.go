package vo

type ScoreMeta struct {
	UserInfo   *UserInfo
	Score      Score
	CreateTime int64 // 达成时间,Unix 秒
}
