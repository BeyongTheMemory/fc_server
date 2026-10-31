package application

import (
	"context"
	"errors"

	"fc_server/internal/domain/record/entity"
)

// ErrUnavailable 记录无法入队(存储不可用或积压已满)→ 503。
var ErrUnavailable = errors.New("game record storage unavailable")

type GameRecordApplication interface {
	// Append 为玩家追加一条游戏记录;data 须是已校验的 JSON 对象。
	// 成功只表示已进入待持久化队列,写入 MySQL 是异步的。
	// clientIP 为上报请求的来源 IP,随记录保存。
	Append(ctx context.Context, userID string, data []byte, clientIP string) (*entity.GameRecord, error)
}
