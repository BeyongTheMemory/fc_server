package dto

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"fc_server/internal/domain/rank/entity/vo"
)

const (
	maxRegionLen = 32
	// illegalPathRune 是 Redis 榜单 key 的路径分隔符,禁止出现在地区字段中
	illegalPathRune = "|"
)

// LocationDto 玩家地区,由客户端上报,决定参与哪些地区榜。
//
// 客户端要求:
//   - 微信 UserInfo 已不再返回 province/city/country,不能从用户信息取地区;
//     需通过 wx.getLocation(须在隐私保护指引中声明位置信息并获得授权)加逆地址解析得到,
//     或由玩家手动选择。
//   - 省、市、区按层级填写:未知的层级留空,只填到已知的最细层级
//     (例如只知道省份就只填 province);市级榜需要省+市,区级榜需要省+市+区。
//   - 每个字段 ≤32 个字符,不能包含 "|"。
type LocationDto struct {
	Province string `json:"province"`
	City     string `json:"city"`
	District string `json:"district"`
}

func (l *LocationDto) ToLocation() *vo.Location {
	if l == nil {
		return &vo.Location{}
	}
	return &vo.Location{
		Province: strings.TrimSpace(l.Province),
		City:     strings.TrimSpace(l.City),
		District: strings.TrimSpace(l.District),
	}
}

// Validate 校验地区字段;location 可省略(不参与地区榜)。
func (l *LocationDto) Validate() error {
	if l == nil {
		return nil
	}
	regions := []struct {
		name  string
		value string
	}{
		{"province", l.Province},
		{"city", l.City},
		{"district", l.District},
	}
	for _, region := range regions {
		if utf8.RuneCountInString(region.value) > maxRegionLen {
			return fmt.Errorf("location.%s exceeds %d characters", region.name, maxRegionLen)
		}
		if strings.Contains(region.value, illegalPathRune) {
			return fmt.Errorf("location.%s contains illegal characters", region.name)
		}
	}
	return nil
}
