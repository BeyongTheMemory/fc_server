package repository

import (
	"context"
	"fc_server/internal/domain/rank/entity/vo"
)

type KeyScore struct {
	Key string
	Score int
}

type RankStorage interface {
	Get(ctx context.Context, location *vo.Location, limit int) (map[string][]KeyScore, error)
	Upload(ctx context.Context, location *vo.Location, key string, score int) error
}
