package dto

import (
	"fc_server/internal/domain/rank/entity"
	"fc_server/internal/domain/rank/entity/vo"
)

type FetchRankListRequest struct {
	UserInfo *UserInfoDto `json:"user_info"`
}

type FetchRankListResponse struct {
	RankList []*RankResultDto `json:"rank_list"`
}

type RankResultDto struct {
	Address       string          `json:"address"`
	AddressLevel  uint16          `json:"address_level"`
	ScoreMetaList []*ScoreMetaDto `json:"score_meta_list"`
}

type ScoreMetaDto struct {
	UserName   string `json:"user_name"`
	Score      int    `json:"score"`
	CreateTime int64  `json:"create_time"`
}

func NewRankResultDto(rankResult *entity.RankResult) *RankResultDto {
	scoreMetaList := make([]*ScoreMetaDto, 0)
	for _, scoreMeta := range rankResult.ScoreMetaList {
		scoreMetaList = append(scoreMetaList, NewScoreMetaDto(scoreMeta))
	}
	return &RankResultDto{
		Address: rankResult.Address,
		AddressLevel: uint16(rankResult.AddressLevel),
		ScoreMetaList: scoreMetaList,
	}
}

func NewScoreMetaDto(scoreMeta *vo.ScoreMeta) *ScoreMetaDto {
	return &ScoreMetaDto{
		UserName: scoreMeta.UserInfo.UserName,
		Score: scoreMeta.Score,
		CreateTime: scoreMeta.CreateTime,
	}
}
