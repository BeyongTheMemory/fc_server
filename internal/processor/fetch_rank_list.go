package processor

import (
	"context"
	"fc_server/internal/domain/rank/application/impl"
	"fc_server/internal/processor/dto"
)

func FetchRankList(ctx context.Context, req *dto.FetchRankListRequest) (*dto.FetchRankListResponse, error) {
	rankApplication := impl.GetRankApplicationImpl()
	rankList, err := rankApplication.GetRank(ctx, req.UserInfo.ToLocation())
	if err != nil {
		return nil, err
	}
	rankListDto := make([]*dto.RankResultDto, 0)
	for _, rankResult := range rankList {
		rankListDto = append(rankListDto, dto.NewRankResultDto(rankResult))
	}
	return &dto.FetchRankListResponse{
		RankList: rankListDto,
	}, nil
}
