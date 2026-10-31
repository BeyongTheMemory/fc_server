package repository

import (
	"context"
	"errors"

	"fc_server/internal/domain/achievement/entity"
)

// ErrQueueFull 待持久化的成就积压已达上限(通常是 MySQL 长时间不可用或未配置)。
var ErrQueueFull = errors.New("achievement queue full")

type AchievementRepository interface {
	// Unlock 原子地:该玩家尚未解锁此成就时保存并加入持久化队列,返回 (a, true);
	// 已解锁时不做任何修改,返回 (首次解锁的记录, false)。
	Unlock(ctx context.Context, a *entity.Achievement) (*entity.Achievement, bool, error)
}
