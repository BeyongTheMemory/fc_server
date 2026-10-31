package entity

import (
	"sort"

	"fc_server/internal/domain/rank/entity/vo"
)

// 榜单时间窗口(specs/001 FR-012):总榜 / 月榜(自然月)/ 周榜(ISO 周)。
const (
	WindowAll   = "all"
	WindowMonth = "month"
	WindowWeek  = "week"
)

// Windows 每类榜单返回的窗口及顺序。
var Windows = []string{WindowAll, WindowMonth, WindowWeek}

// 榜单范围:天榜(全国)、地榜(城市,人数不足时为省份)、人榜(好友)。
const (
	ScopeGlobal   = "global"
	ScopeProvince = "province"
	ScopeCity     = "city"
	ScopeFriend   = "friend"
)

// RankResult 某一范围、某一时间窗口的榜单及请求玩家在其中的名次。
type RankResult struct {
	Scope         string
	Address       string // 地榜为省/市名,其余为空
	AddressLevel  vo.AddressLevel
	Window        string // WindowAll / WindowMonth / WindowWeek
	ScoreMetaList []*vo.ScoreMeta
	SelfRank      int64    // 1 起;0 表示未上榜
	SelfScore     vo.Score // 未上榜为零值
}

func NewRankResult(address string, addressLevel vo.AddressLevel) *RankResult {
	return &RankResult{
		Scope:         scopeOf(addressLevel),
		Address:       address,
		AddressLevel:  addressLevel,
		ScoreMetaList: []*vo.ScoreMeta{},
	}
}

func scopeOf(level vo.AddressLevel) string {
	switch level {
	case vo.Nation:
		return ScopeGlobal
	case vo.Province:
		return ScopeProvince
	default:
		return ScopeCity
	}
}

// Leaderboards 一次查询返回的三类榜单,每类按 Windows 顺序各一个窗口。
type Leaderboards struct {
	Global []*RankResult // 天榜
	Region []*RankResult // 地榜;玩家未提供省份时为 nil
	Friend []*RankResult // 人榜;尚未同步好友时为 nil
}

// NormalizeOrder 按成绩降序排列(见 vo.Score),成绩相同先达成者(CreateTime 小)在前。
func (r *RankResult) NormalizeOrder() {
	sort.SliceStable(r.ScoreMetaList, func(i, j int) bool {
		a, b := r.ScoreMetaList[i], r.ScoreMetaList[j]
		if c := a.Score.Compare(b.Score); c != 0 {
			return c > 0
		}
		return a.CreateTime < b.CreateTime
	})
}
