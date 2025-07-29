package dto

import (
	"fc_server/internal/domain/rank/entity/vo"
)

type UserInfoDto struct {
	UserId   string `json:"user_id"`
	UserName string `json:"user_name"`
	*LocationDto `json:"location"`
}

func (u *UserInfoDto) ToUserInfo() *vo.UserInfo {
	return &vo.UserInfo{
		UserId:   u.UserId,
		UserName: u.UserName,
	}
}


type LocationDto struct {
	Province string `json:"province"`
	City     string `json:"city"`
	District string `json:"district"`
}

func (l *LocationDto) ToLocation() *vo.Location {
	return &vo.Location{
		Province: l.Province,
		City:     l.City,
		District: l.District,
	}
}