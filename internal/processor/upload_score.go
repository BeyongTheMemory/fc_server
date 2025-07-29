package processor

import (
	"context"
	"fc_server/internal/domain/rank/application/impl"
	"fc_server/internal/processor/dto"
)

func UploadScore(ctx context.Context, request *dto.UploadScoreRequest) (*dto.UploadScoreResponse, error) {
	rankApplication := impl.GetRankApplicationImpl()
	err := rankApplication.UploadScore(ctx, request.UserInfo.ToLocation(), request.UserInfo.ToUserInfo(), request.Score)
	if err != nil {
		return nil, err
	}
	return &dto.UploadScoreResponse{}, nil
}
