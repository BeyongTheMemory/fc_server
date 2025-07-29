package application

import (
	"context"

	"fc_server/internal/domain/rank/entity"
	"fc_server/internal/domain/rank/entity/vo"
)

type RankApplication interface {
	GetRank(ctx context.Context, location *vo.Location) ([]*entity.RankResult, error)
	UploadScore(ctx context.Context, location *vo.Location, UserInfo *vo.UserInfo, Score int) error
}
