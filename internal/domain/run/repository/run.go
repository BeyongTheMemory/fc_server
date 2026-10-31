package repository

import (
	"context"
	"errors"
	"time"

	"fc_server/internal/domain/run/entity"
)

// ErrRunNotFound 对局票不存在或已过期。
var ErrRunNotFound = errors.New("run not found")

// ClaimResult 占用成绩提交名额的结果。
type ClaimResult int

const (
	ClaimNew      ClaimResult = iota // 首次提交
	ClaimReplay                      // 已提交过完全相同的成绩(客户端重试)
	ClaimConflict                    // 已提交过不同的成绩
)

type RunRepository interface {
	// Create 保存对局票,ttl 后过期。
	Create(ctx context.Context, run *entity.Run, ttl time.Duration) error
	// Get 读取对局票;不存在或已过期返回 ErrRunNotFound。
	Get(ctx context.Context, runID string) (*entity.Run, error)
	// ClaimSubmission 原子地占用本局唯一的成绩提交名额;fingerprint 唯一标识提交的成绩。
	// 对局票不存在返回 ErrRunNotFound。
	ClaimSubmission(ctx context.Context, runID, fingerprint string, nowMs int64) (ClaimResult, error)
}

// StartLimiter 开局频率限制(固定窗口计数)。
type StartLimiter interface {
	Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error)
}
