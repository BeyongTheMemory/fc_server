package impl

import (
	"context"
	"fmt"

	"fc_server/internal/domain/rank/application"
	"fc_server/internal/domain/rank/entity"
	"fc_server/internal/domain/rank/entity/vo"
	"fc_server/internal/domain/rank/repository"
)
const (
	rankLimit = 10
)

var (
	rankApplicationImpl *RankApplicationImpl
	_ application.RankApplication = (*RankApplicationImpl)(nil)
)

func Init(rankStorage repository.RankStorage) {
	rankApplicationImpl = &RankApplicationImpl{
		rankStorage: rankStorage,
	}
}

func GetRankApplicationImpl() application.RankApplication {
	if rankApplicationImpl == nil {
		panic("rankApplicationImpl is not initialized")
	}
	return rankApplicationImpl
}

type RankApplicationImpl struct {
	rankStorage repository.RankStorage
}

func (r *RankApplicationImpl) GetRank(ctx context.Context, location *vo.Location) ([]*entity.RankResult, error) {
	 result, err := r.rankStorage.Get(ctx, location, rankLimit)
	 if err != nil {
		return nil, err
	 }
	 rankResults := make([]*entity.RankResult, 0)
	 for address, keyScores := range result {
		addressLevel := vo.Unknown
		switch address {
		case location.Province:
			addressLevel = vo.Province
		case location.City:
			addressLevel = vo.City
		case location.District:
			addressLevel = vo.District
		}
		if addressLevel == vo.Unknown {
			return nil, fmt.Errorf("address level is not correct, address: %s", address)
		}



		rankResult := entity.NewRankResult(address, addressLevel)
		for _, keyScore := range keyScores {
			err := rankResult.AddScoreMetaFromKey(keyScore.Key, keyScore.Score)
			if err != nil {
				return nil, err
			}
		}
		rankResults = append(rankResults, rankResult)
	 }
	 return rankResults, nil
}

func (r *RankApplicationImpl) UploadScore(ctx context.Context, location *vo.Location, UserInfo *vo.UserInfo, Score int) error {
	rankResult := entity.NewRankResult(location.Province, vo.Province)
	scoreMeta := &vo.ScoreMeta{
		UserInfo: UserInfo,
		Score: Score,
	}
	key := rankResult.AddNewScoreMetaAndReturnKey(scoreMeta)
	return r.rankStorage.Upload(ctx, location, key, Score)
}