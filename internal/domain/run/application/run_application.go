package application

import (
	"context"
	"errors"

	"fc_server/internal/domain/run/entity"
)

var (
	ErrRunNotFound = errors.New("invalid or expired run_id")                            // → 400
	ErrRunConflict = errors.New("a different score was already submitted for this run") // → 409
	ErrRunTooFast  = errors.New("run finished faster than possible")                    // → 400
	ErrTooManyRuns = errors.New("too many runs started")                                // → 429
	ErrUnavailable = errors.New("run storage unavailable")                              // → 503
)

type RunApplication interface {
	// Start 签发对局票,种子由服务端生成。
	Start(ctx context.Context, userID string) (*entity.Run, error)
	// AuthorizeScore 校验玩家凭 runID 上报到达 stage 关的成绩是否被允许,并占用本局唯一的提交名额。
	// fingerprint 唯一标识这份成绩(同一成绩重试返回成功,不同成绩返回 ErrRunConflict)。
	AuthorizeScore(ctx context.Context, userID, runID string, stage int64, fingerprint string) error
}
