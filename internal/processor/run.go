package processor

import (
	"context"

	"fc_server/internal/domain/run/application/impl"
	"fc_server/internal/processor/dto"
)

// StartRun 为已认证玩家签发一张对局票(种子由服务端生成)。
func StartRun(ctx context.Context, userID string) (*dto.StartRunResponse, error) {
	run, err := impl.GetRunApplicationImpl().Start(ctx, userID)
	if err != nil {
		return nil, err
	}
	return dto.NewStartRunResponse(run), nil
}
