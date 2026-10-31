package repository

import (
	"context"

	"fc_server/internal/domain/rank/entity"
	"fc_server/internal/domain/rank/entity/vo"
)

// BoardRef 指定一张榜单:时间窗口 + 范围。Level 为 vo.Nation 时是全国榜,忽略 Location;
// 窗口对应的具体期号(本周/本月)由存储实现按当前时间确定。
type BoardRef struct {
	Window   string
	Level    vo.AddressLevel
	Location *vo.Location
}

type RankStorage interface {
	// UploadBest 仅当 score 优于该玩家历史最好成绩(vo.Score.Compare)时更新全国榜、各层级地区榜
	// 与元数据,返回是否更新及当前历史最好成绩。重复上报幂等。周/月榜按当期各自比较。
	// clientIP 为上报请求的来源 IP,随刷新纪录的成绩一起保存(用于事后排查刷榜)。
	UploadBest(ctx context.Context, location *vo.Location, user *vo.UserInfo, score vo.Score, clientIP string) (updated bool, best vo.Score, err error)
	// CountMembers 返回各榜单的人数;路径不全的地区榜为 0。
	CountMembers(ctx context.Context, refs []BoardRef) ([]int64, error)
	// GetBoards 按 refs 顺序返回各榜单前 limit 名(按成绩降序,同成绩先达成者在前)
	// 及 userId 在其中的名次与成绩。
	GetBoards(ctx context.Context, refs []BoardRef, userId string, limit int) ([]*entity.RankResult, error)
	// GetMemberBests 读取指定玩家在某窗口当期的最好成绩(顺序不保证);当期没有成绩的玩家不在结果中。
	GetMemberBests(ctx context.Context, window string, userIds []string) ([]*vo.ScoreMeta, error)
}
