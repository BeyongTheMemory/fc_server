package repository

import (
	"context"
	"time"
)

// FriendRepository 保存玩家的同玩互动好友(以好友的 user_id 表示,不保存好友 openid)。
type FriendRepository interface {
	// Replace 用最新同步结果整体替换好友列表,ttl 后过期需重新同步;friendIDs 可以为空。
	Replace(ctx context.Context, userID string, friendIDs []string, ttl time.Duration) error
	// List 返回好友 user_id;synced=false 表示从未同步或已过期。
	List(ctx context.Context, userID string) (friendIDs []string, synced bool, err error)
}
