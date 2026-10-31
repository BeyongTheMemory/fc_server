package repository_impl

import (
	"context"
	"fmt"
	"time"

	"fc_server/internal/domain/person/repository"

	"github.com/redis/go-redis/v9"
)

// 好友列表: friends:{user_id}  SET,成员为好友 user_id,另含玩家自己作为"已同步"标记
// (好友为空时集合也存在);TTL 为好友有效期。
const keyPrefixFriends = "friends:"

func friendsKey(userID string) string { return keyPrefixFriends + userID }

type FriendRedisStorage struct {
	client *redis.Client
}

var _ repository.FriendRepository = (*FriendRedisStorage)(nil)

func NewFriendRedisStorage(client *redis.Client) *FriendRedisStorage {
	return &FriendRedisStorage{client: client}
}

func (s *FriendRedisStorage) Replace(ctx context.Context, userID string, friendIDs []string, ttl time.Duration) error {
	key := friendsKey(userID)
	members := make([]interface{}, 0, len(friendIDs)+1)
	members = append(members, userID)
	for _, id := range friendIDs {
		members = append(members, id)
	}
	_, err := s.client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Del(ctx, key)
		pipe.SAdd(ctx, key, members...)
		pipe.Expire(ctx, key, ttl)
		return nil
	})
	if err != nil {
		return fmt.Errorf("replace friends: %w", err)
	}
	return nil
}

func (s *FriendRedisStorage) List(ctx context.Context, userID string) ([]string, bool, error) {
	members, err := s.client.SMembers(ctx, friendsKey(userID)).Result()
	if err != nil {
		return nil, false, fmt.Errorf("list friends: %w", err)
	}
	if len(members) == 0 {
		return nil, false, nil
	}
	friends := make([]string, 0, len(members))
	for _, m := range members {
		if m != userID {
			friends = append(friends, m)
		}
	}
	return friends, true, nil
}
