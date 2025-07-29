package entity

import (
	"fc_server/internal/domain/rank/entity/vo"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type RankResult struct {
	Address       string
	AddressLevel  vo.AddressLevel
	ScoreMetaList []*vo.ScoreMeta
}

const (
	keyFormat = "%s:%s:%d" // userId:userName:createTime
)

func NewRankResult(address string, addressLevel vo.AddressLevel) *RankResult {
	return &RankResult{
		Address:       address,
		AddressLevel:  addressLevel,
		ScoreMetaList: []*vo.ScoreMeta{},
	}
}

func (r *RankResult) AddScoreMetaFromKey(key string, score int) error {
	result := strings.Split(key, ":")
	if len(result) != 3 {
		return fmt.Errorf("key format is not correct, expected lenth is 3, got: %d, key: %s", len(result), key)
	}
	createTime, err := strconv.ParseInt(result[2], 10, 64)
	if err != nil {
		return fmt.Errorf("createTime format is not correct, key: %s", key)
	}
	r.ScoreMetaList = append(r.ScoreMetaList, &vo.ScoreMeta{
		UserInfo: &vo.UserInfo{
			UserId:   result[0],
			UserName: result[1],
		},
		Score:      score,
		CreateTime: createTime,
	})
	return nil
}

func (r *RankResult) AddNewScoreMetaAndReturnKey(scoreMeta *vo.ScoreMeta) string {
	scoreMeta.CreateTime = time.Now().Unix()
	r.ScoreMetaList = append(r.ScoreMetaList, scoreMeta)
	return fmt.Sprintf(keyFormat, scoreMeta.UserInfo.UserId, scoreMeta.UserInfo.UserName, scoreMeta.CreateTime)
}
