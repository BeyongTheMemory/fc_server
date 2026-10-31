package application

import (
	"context"

	"fc_server/internal/domain/rank/entity"
	"fc_server/internal/domain/rank/entity/vo"
)

type RankApplication interface {
	// UploadScore 上报一局成绩,仅当优于历史最好成绩时更新,返回是否更新与当前最好成绩。
	// clientIP 为上报请求的来源 IP,随刷新纪录的成绩一起保存。
	UploadScore(ctx context.Context, location *vo.Location, user *vo.UserInfo, score vo.Score, clientIP string) (updated bool, best vo.Score, err error)
	// GetRank 返回天榜(全国)、地榜(城市,城市人数不足时为省份)与人榜(好友),
	// 每类含总/月/周三个窗口,各取前 N 名(同分先达成者在前)及玩家自己的名次。
	// friendIds 为 nil 表示尚未同步好友,不计算人榜;空切片表示没有好友,人榜只含自己。
	GetRank(ctx context.Context, location *vo.Location, userId string, friendIds []string) (*entity.Leaderboards, error)
}
