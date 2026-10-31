package dto

import (
	"errors"

	"fc_server/internal/domain/person/entity"
	rankentity "fc_server/internal/domain/rank/entity"
)

// FetchRankListRequest POST /fetch_rank,需要 `Authorization: Bearer <token>`。
//
//	{"location":{"province":"浙江省","city":"杭州市","district":"西湖区"}}
//
// 一次返回三类榜单,每类含总榜(all)/本月(month)/本周(week)三个窗口:
//   - global(天榜):全国所有玩家。
//   - region(地榜):玩家所在城市;某个窗口里该城市人数少于 REGION_MIN_PLAYERS(默认 10)时,
//     该窗口改为省份榜。看每个榜的 scope("city"/"province")与 address 决定标题,
//     例如 "杭州市榜" / "浙江省榜"。没有上报省份时 region 为 null。
//   - friend(人榜):自己与同玩互动好友,需先调用 POST /friends/sync;从未同步或同步已过期(30 天)
//     时 friend 为 null,客户端应先同步再刷新;已同步但好友都没有成绩时只有自己。
//
// 客户端要求:
//   - location 规则见 LocationDto;城市/省份变化后下次查询自动按新地区返回。
//   - 三类榜单一起返回,切换 tab 不需要重复请求;返回结果可缓存几十秒,不要每帧请求。
//   - 排名展示用 score_meta_list 中的 rank;自己的名次看 self_rank(0 = 未上榜)与 self_score。
type FetchRankListRequest struct {
	Location *LocationDto `json:"location"`
}

func (r *FetchRankListRequest) Validate() error {
	if r == nil {
		return errors.New("request body is required")
	}
	return r.Location.Validate()
}

// FetchRankListResponse hidden=true 时(服务端配置 LEADERBOARD_HIDDEN)三类榜单均为 null。
//
// 客户端要求:
//   - 读到 hidden=true 时隐藏整个排行榜(入口按钮、页面与“我的排名”都不显示),不要显示空榜或报错;
//     不要缓存该状态到下次启动,每次进入排行榜前以接口返回为准。
//   - hidden=false 时按 global/region/friend 正常展示。
type FetchRankListResponse struct {
	Hidden bool         `json:"hidden"`
	Global *BoardSetDto `json:"global"`
	Region *BoardSetDto `json:"region"`
	Friend *BoardSetDto `json:"friend"`
}

// BoardSetDto 一类榜单的三个时间窗口。
type BoardSetDto struct {
	All   *RankResultDto `json:"all"`
	Month *RankResultDto `json:"month"`
	Week  *RankResultDto `json:"week"`
}

type RankResultDto struct {
	Scope         string          `json:"scope"`         // global / city / province / friend
	Address       string          `json:"address"`       // 地榜为城市或省份名,其余为空
	AddressLevel  uint16          `json:"address_level"` // 0 全国 1 省 2 市;人榜为 0
	Window        string          `json:"window"`        // all=总榜 month=本月 week=本周
	SelfRank      int64           `json:"self_rank"`     // 自己的名次,1 起;0 表示未上榜
	SelfScore     *ScoreDto       `json:"self_score"`    // 自己在该榜的成绩;未上榜为 null
	ScoreMetaList []*ScoreMetaDto `json:"score_meta_list"`
}

// ScoreMetaDto 榜单中的一名玩家,已按排名规则排好序。user_name/avatar_url 取自该玩家当前资料,
// 改名换头像后对所有历史榜单立即生效。
//
// 客户端要求:
//   - user_name 即最终展示名(未授权为“无名”),直接显示,过长时由客户端省略。
//   - avatar_url 为空时显示默认头像;加载失败(该玩家更换头像后旧 URL 失效)同样回退默认头像,
//     不要阻塞榜单渲染。
//   - avatar_url 末尾数字是正方形尺寸(0/46/64/96/132,0 为 640px),列表中可替换为 /64 节省流量。
//   - 远程加载头像需在小游戏后台把头像域名(如 thirdwx.qlogo.cn)加入 downloadFile 合法域名,
//     以真机加载结果为准。
type ScoreMetaDto struct {
	Rank             int    `json:"rank"`
	UserId           string `json:"user_id"`
	UserName         string `json:"user_name"`
	AvatarUrl        string `json:"avatar_url"`
	Stage            int64  `json:"stage"`
	StageCultivation string `json:"stage_cultivation"` // 十进制字符串,见 UploadScoreRequest 的数值协议
	TotalCultivation string `json:"total_cultivation"`
	CreateTime       int64  `json:"create_time"` // 达成时间,Unix 秒
}

// NewBoardSetDto boards 按 rankentity.Windows 顺序;nil 表示该类榜单不存在,返回 nil。
func NewBoardSetDto(boards []*rankentity.RankResult, players map[string]*entity.Person) *BoardSetDto {
	if boards == nil {
		return nil
	}
	set := &BoardSetDto{}
	for _, b := range boards {
		dto := newRankResultDto(b, players)
		switch b.Window {
		case rankentity.WindowAll:
			set.All = dto
		case rankentity.WindowMonth:
			set.Month = dto
		case rankentity.WindowWeek:
			set.Week = dto
		}
	}
	return set
}

// newRankResultDto players 为榜单成员的当前资料;不在其中的成员(无玩家记录的历史数据)
// 回退为榜单里保存的昵称、空头像。
func newRankResultDto(rankResult *rankentity.RankResult, players map[string]*entity.Person) *RankResultDto {
	scoreMetaList := make([]*ScoreMetaDto, 0, len(rankResult.ScoreMetaList))
	for i, scoreMeta := range rankResult.ScoreMetaList {
		item := &ScoreMetaDto{
			Rank:             i + 1,
			UserId:           scoreMeta.UserInfo.UserId,
			UserName:         scoreMeta.UserInfo.UserName,
			Stage:            scoreMeta.Score.Stage,
			StageCultivation: scoreMeta.Score.StageCultivation.String(),
			TotalCultivation: scoreMeta.Score.TotalCultivation.String(),
			CreateTime:       scoreMeta.CreateTime,
		}
		if p, ok := players[item.UserId]; ok {
			item.UserName = p.DisplayName()
			item.AvatarUrl = p.Profile.AvatarURL
		}
		scoreMetaList = append(scoreMetaList, item)
	}
	return &RankResultDto{
		Scope:         rankResult.Scope,
		Address:       rankResult.Address,
		AddressLevel:  uint16(rankResult.AddressLevel),
		Window:        rankResult.Window,
		SelfRank:      rankResult.SelfRank,
		SelfScore:     NewScoreDto(rankResult.SelfScore),
		ScoreMetaList: scoreMetaList,
	}
}
