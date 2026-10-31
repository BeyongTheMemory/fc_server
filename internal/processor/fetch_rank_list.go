package processor

import (
	"context"
	"fmt"

	personimpl "fc_server/internal/domain/person/application/impl"
	"fc_server/internal/domain/rank/application/impl"
	rankentity "fc_server/internal/domain/rank/entity"
	"fc_server/internal/processor/dto"
)

// FetchRankList 返回已认证玩家的天榜、地榜与人榜;"我的排名"只按登录身份计算,
// 榜单成员的昵称头像按其当前资料批量补全。
func FetchRankList(ctx context.Context, userID string, req *dto.FetchRankListRequest) (*dto.FetchRankListResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidParam, err.Error())
	}
	if leaderboardHide {
		return &dto.FetchRankListResponse{Hidden: true}, nil
	}
	users := personimpl.GetUserApplicationImpl()
	friendIDs, err := users.GetFriendIDs(ctx, userID)
	if err != nil {
		return nil, err
	}
	boards, err := impl.GetRankApplicationImpl().GetRank(ctx, req.Location.ToLocation(), userID, friendIDs)
	if err != nil {
		return nil, err
	}
	all := make([]*rankentity.RankResult, 0, len(boards.Global)+len(boards.Region)+len(boards.Friend))
	all = append(append(append(all, boards.Global...), boards.Region...), boards.Friend...)
	players, err := users.GetPlayers(ctx, memberIDs(all))
	if err != nil {
		return nil, err
	}
	return &dto.FetchRankListResponse{
		Global: dto.NewBoardSetDto(boards.Global, players),
		Region: dto.NewBoardSetDto(boards.Region, players),
		Friend: dto.NewBoardSetDto(boards.Friend, players),
	}, nil
}

// memberIDs 各榜单成员去重后的 user_id(同一玩家通常同时出现在多个层级与窗口)。
func memberIDs(boards []*rankentity.RankResult) []string {
	seen := map[string]struct{}{}
	ids := make([]string, 0)
	for _, board := range boards {
		for _, meta := range board.ScoreMetaList {
			id := meta.UserInfo.UserId
			if _, ok := seen[id]; ok || id == "" {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	return ids
}
