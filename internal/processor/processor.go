package processor

import "errors"

// ErrInvalidParam 标记参数校验失败,HTTP 层据此返回 400(contracts/http-api.md)。
var ErrInvalidParam = errors.New("invalid param")

var (
	maxStage        int64 = 100_000
	leaderboardHide bool
)

// Init 注入配置项:关卡数上报上限(修为不设数值上限,仅限位数,见 vo.MaxCultivationDigits),
// 以及是否隐藏排行榜(fetch_rank 返回 hidden=true 且不返回任何榜单)。
func Init(maxStageLimit int64, hideLeaderboard bool) {
	if maxStageLimit > 0 {
		maxStage = maxStageLimit
	}
	leaderboardHide = hideLeaderboard
}
