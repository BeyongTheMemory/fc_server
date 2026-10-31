package repository

import (
	"context"
	"errors"

	"fc_server/internal/domain/person/entity"
	"fc_server/internal/domain/person/entity/vo"
)

var (
	// ErrPlayerNotFound 玩家记录不存在。
	ErrPlayerNotFound = errors.New("player not found")
	// ErrIdentityMismatch 已存在的玩家记录归属于另一个微信身份(不应发生,视为数据损坏)。
	ErrIdentityMismatch = errors.New("player identity mismatch")
)

type PlayerRepository interface {
	// UpsertOnLogin 原子地:不存在则以默认资料创建;存在则只刷新登录时间,
	// 不覆盖已有昵称头像。两种情况都递增 revision 并标记待备份。
	UpsertOnLogin(ctx context.Context, userID, appID, openID string, now int64) (*entity.Person, error)
	// Get 读取玩家;不存在返回 ErrPlayerNotFound。
	Get(ctx context.Context, userID string) (*entity.Person, error)
	// GetMany 批量读取玩家,返回 user_id → 玩家;不存在的 ID 不出现在结果中。
	GetMany(ctx context.Context, userIDs []string) (map[string]*entity.Person, error)
	// UpdateProfile 原子更新资料;与现有资料相同时不递增 revision、不重复标脏。
	// 玩家不存在返回 ErrPlayerNotFound。
	UpdateProfile(ctx context.Context, userID string, profile vo.Profile, now int64) (*entity.Person, error)
}
