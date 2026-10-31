package dto

import (
	"errors"
	"regexp"

	"fc_server/internal/domain/run/entity"
)

var runIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// StartRunResponse POST /runs/start 的响应,需要 `Authorization: Bearer <token>`,无请求体。
// 每开一局要上榜的对局(普通局)调用一次,领取对局票。
//
//	{"run_id":"<32 位十六进制>","seed":"Ab3dE9xQ","started_at_ms":1793380000000,"expires_at_ms":1795972000000}
//
// 客户端要求:
//   - 普通局必须用响应里的 seed 初始化随机流(GameRandom.initialize(seed)),不能用本地生成的种子。
//   - 梦蝶局(玩家自行输入种子)不调用本接口,结束时也不调用 /upload_score。
//   - 把 run_id 与种子一起存进本局存档(five_card_save_v1),续局后结束时用同一个 run_id 上报;
//     对局票有效期见 expires_at_ms(默认 30 天),过期后该局成绩无法上报。
//   - 只在真正开新局时调用;读档续局不要重新领票(新票的种子不同,也会让旧局成绩无法提交)。
//   - 开局有频率限制(默认每小时 60 次),超出返回 429,此时可以提示稍后再试。
//   - 领票失败(网络、5xx、429)时可以让玩家离线开局,但这一局没有 run_id,结束时不上报成绩。
type StartRunResponse struct {
	RunId       string `json:"run_id"`
	Seed        string `json:"seed"`
	StartedAtMs int64  `json:"started_at_ms"`
	ExpiresAtMs int64  `json:"expires_at_ms"`
}

func NewStartRunResponse(run *entity.Run) *StartRunResponse {
	return &StartRunResponse{RunId: run.ID, Seed: run.Seed, StartedAtMs: run.StartedAtMs, ExpiresAtMs: run.ExpiresAtMs}
}

func validateRunID(id string) error {
	if !runIDPattern.MatchString(id) {
		return errors.New("run_id is required (from POST /runs/start)")
	}
	return nil
}
