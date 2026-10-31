package impl

import (
	"context"

	"fc_server/internal/domain/rank/application"
	"fc_server/internal/domain/rank/entity"
	"fc_server/internal/domain/rank/entity/vo"
	"fc_server/internal/domain/rank/repository"
)

var (
	rankApplicationImpl *RankApplicationImpl
	_                   application.RankApplication = (*RankApplicationImpl)(nil)
)

// Init rankLimit 每张榜的长度;regionMinPlayers 城市榜至少需要的人数,不足时地榜退回省份榜。
func Init(rankStorage repository.RankStorage, rankLimit, regionMinPlayers int) {
	rankApplicationImpl = &RankApplicationImpl{
		rankStorage:      rankStorage,
		rankLimit:        rankLimit,
		regionMinPlayers: regionMinPlayers,
	}
}

func GetRankApplicationImpl() application.RankApplication {
	if rankApplicationImpl == nil {
		panic("rankApplicationImpl is not initialized")
	}
	return rankApplicationImpl
}

type RankApplicationImpl struct {
	rankStorage      repository.RankStorage
	rankLimit        int
	regionMinPlayers int
}

func (r *RankApplicationImpl) UploadScore(ctx context.Context, location *vo.Location, user *vo.UserInfo, score vo.Score, clientIP string) (bool, vo.Score, error) {
	return r.rankStorage.UploadBest(ctx, location, user, score, clientIP)
}

func (r *RankApplicationImpl) GetRank(ctx context.Context, location *vo.Location, userId string, friendIds []string) (*entity.Leaderboards, error) {
	refs := make([]repository.BoardRef, 0, 2*len(entity.Windows))
	for _, w := range entity.Windows {
		refs = append(refs, repository.BoardRef{Window: w, Level: vo.Nation})
	}
	regionRefs, err := r.regionRefs(ctx, location)
	if err != nil {
		return nil, err
	}
	refs = append(refs, regionRefs...)

	boards, err := r.rankStorage.GetBoards(ctx, refs, userId, r.rankLimit)
	if err != nil {
		return nil, err
	}
	for _, board := range boards {
		board.NormalizeOrder()
	}
	n := len(entity.Windows)
	result := &entity.Leaderboards{Global: boards[:n]}
	if len(boards) > n {
		result.Region = boards[n:]
	}
	if friendIds != nil {
		if result.Friend, err = r.friendBoards(ctx, userId, friendIds); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// regionRefs 地榜:每个窗口独立判断,城市榜人数达到阈值用城市榜,否则用省份榜;
// 只知道省份时直接用省份榜;连省份都没有则没有地榜。
func (r *RankApplicationImpl) regionRefs(ctx context.Context, location *vo.Location) ([]repository.BoardRef, error) {
	if location == nil || location.Province == "" {
		return nil, nil
	}
	refs := make([]repository.BoardRef, len(entity.Windows))
	for i, w := range entity.Windows {
		refs[i] = repository.BoardRef{Window: w, Level: vo.Province, Location: location}
	}
	if location.City == "" {
		return refs, nil
	}
	cityRefs := make([]repository.BoardRef, len(entity.Windows))
	for i, w := range entity.Windows {
		cityRefs[i] = repository.BoardRef{Window: w, Level: vo.City, Location: location}
	}
	counts, err := r.rankStorage.CountMembers(ctx, cityRefs)
	if err != nil {
		return nil, err
	}
	for i := range refs {
		if counts[i] >= int64(r.regionMinPlayers) {
			refs[i] = cityRefs[i]
		}
	}
	return refs, nil
}

// friendBoards 人榜:好友与自己当期成绩排序;自己名次在截断前计算,排在前 N 之外也能看到。
func (r *RankApplicationImpl) friendBoards(ctx context.Context, userId string, friendIds []string) ([]*entity.RankResult, error) {
	members := make([]string, 0, len(friendIds)+1)
	seen := map[string]struct{}{}
	for _, id := range append([]string{userId}, friendIds...) {
		if _, ok := seen[id]; ok || id == "" {
			continue
		}
		seen[id] = struct{}{}
		members = append(members, id)
	}

	boards := make([]*entity.RankResult, 0, len(entity.Windows))
	for _, w := range entity.Windows {
		metas, err := r.rankStorage.GetMemberBests(ctx, w, members)
		if err != nil {
			return nil, err
		}
		board := &entity.RankResult{Scope: entity.ScopeFriend, Window: w, ScoreMetaList: metas}
		board.NormalizeOrder()
		for i, m := range board.ScoreMetaList {
			if m.UserInfo.UserId == userId {
				board.SelfRank, board.SelfScore = int64(i+1), m.Score
				break
			}
		}
		if len(board.ScoreMetaList) > r.rankLimit {
			board.ScoreMetaList = board.ScoreMetaList[:r.rankLimit]
		}
		boards = append(boards, board)
	}
	return boards, nil
}
