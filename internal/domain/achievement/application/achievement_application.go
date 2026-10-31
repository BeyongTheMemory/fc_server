package application

import (
	"context"
	"errors"

	"fc_server/internal/domain/achievement/entity"
)

// ErrUnavailable 成就无法保存(存储不可用或积压已满)→ 503。
var ErrUnavailable = errors.New("achievement storage unavailable")

type AchievementApplication interface {
	// Unlock 为玩家解锁成就;unlockedAtMs 为 0 时使用服务端时间,detail 为已校验的 JSON 对象或 nil。
	// 返回生效的成就记录(重复解锁时为首次那条)与本次是否为首次解锁。
	// clientIP 为上报请求的来源 IP,随首次解锁记录保存。
	Unlock(ctx context.Context, userID, achievementID string, unlockedAtMs int64, detail []byte, clientIP string) (*entity.Achievement, bool, error)
}
