package repository

import (
	"context"
	"errors"
	"time"

	"fc_server/internal/domain/person/entity"
)

// ErrSessionNotFound 会话不存在或已过期。
var ErrSessionNotFound = errors.New("session not found")

// SessionRepository 以 token 哈希为键保存业务会话;实现方从不接触明文 token。
type SessionRepository interface {
	Create(ctx context.Context, tokenHash string, session *entity.Session, ttl time.Duration) error
	// Get 不存在或已过期返回 ErrSessionNotFound。
	Get(ctx context.Context, tokenHash string) (*entity.Session, error)
	Delete(ctx context.Context, tokenHash string) error
}

// AttemptLimiter 固定窗口计数限流,窗口键自带过期。
type AttemptLimiter interface {
	// Allow 记录一次尝试,返回本窗口内是否仍在 limit 以内。
	Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error)
}
