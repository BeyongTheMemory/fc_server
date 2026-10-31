package repository_impl

import (
	"context"
	"encoding/json"
	"fmt"

	"fc_server/internal/domain/achievement/entity"
	"fc_server/internal/domain/achievement/repository"

	"github.com/redis/go-redis/v9"
)

// Redis key 方案(成就):
//
//	已解锁:       achievements:{user_id}    HASH achievement_id → 首次解锁信息(JSON)
//	待持久化队列: achievement:queue         LIST,首次解锁时追加
//	消费锁:       achievement:flush_lock
const (
	keyPrefixAchievements   = "achievements:"
	keyAchievementQueue     = "achievement:queue"
	keyAchievementFlushLock = "achievement:flush_lock"
)

// luaUnlockAchievement 已解锁则原样返回首次记录;否则在积压未满时保存并入队。
// 返回 {1, 新记录} / {0, 已有记录} / {-1, ""}(积压已满)。
const luaUnlockAchievement = `
local existing = redis.call('HGET', KEYS[1], ARGV[1])
if existing then
  return {0, existing}
end
if redis.call('LLEN', KEYS[2]) >= tonumber(ARGV[4]) then
  return {-1, ''}
end
redis.call('HSET', KEYS[1], ARGV[1], ARGV[2])
redis.call('RPUSH', KEYS[2], ARGV[3])
return {1, ARGV[2]}
`

var unlockAchievementScript = redis.NewScript(luaUnlockAchievement)

// storedAchievement 已解锁哈希中的值。
type storedAchievement struct {
	UnlockedAtMs int64           `json:"unlocked_at_ms"`
	ReceivedAtMs int64           `json:"received_at_ms"`
	ClientIP     string          `json:"client_ip,omitempty"`
	Detail       json.RawMessage `json:"detail,omitempty"`
}

// queuedAchievement 持久化队列中的元素。
type queuedAchievement struct {
	UserID        string `json:"user_id"`
	AchievementID string `json:"achievement_id"`
	storedAchievement
}

type AchievementRedisStorage struct {
	lockedListQueue
}

var _ repository.AchievementRepository = (*AchievementRedisStorage)(nil)

func NewAchievementRedisStorage(client *redis.Client, maxQueueLen int) *AchievementRedisStorage {
	return &AchievementRedisStorage{lockedListQueue{
		client: client, key: keyAchievementQueue, lockKey: keyAchievementFlushLock, maxLen: maxQueueLen, name: "achievement",
	}}
}

func achievementsKey(userID string) string { return keyPrefixAchievements + userID }

func (s *AchievementRedisStorage) Unlock(ctx context.Context, a *entity.Achievement) (*entity.Achievement, bool, error) {
	stored := storedAchievement{UnlockedAtMs: a.UnlockedAtMs, ReceivedAtMs: a.ReceivedAtMs, ClientIP: a.ClientIP, Detail: a.Detail}
	value, err := json.Marshal(stored)
	if err != nil {
		return nil, false, fmt.Errorf("encode achievement: %w", err)
	}
	item, err := json.Marshal(queuedAchievement{UserID: a.UserID, AchievementID: a.AchievementID, storedAchievement: stored})
	if err != nil {
		return nil, false, fmt.Errorf("encode achievement: %w", err)
	}
	res, err := unlockAchievementScript.Run(ctx, s.client, []string{achievementsKey(a.UserID), keyAchievementQueue},
		a.AchievementID, value, item, s.maxLen).Slice()
	if err != nil {
		return nil, false, fmt.Errorf("unlock achievement: %w", err)
	}
	if len(res) != 2 {
		return nil, false, fmt.Errorf("unlock achievement: unexpected script reply %v", res)
	}
	status, _ := res[0].(int64)
	if status == -1 {
		return nil, false, repository.ErrQueueFull
	}
	raw, _ := res[1].(string)
	var saved storedAchievement
	if err := json.Unmarshal([]byte(raw), &saved); err != nil {
		return nil, false, fmt.Errorf("decode achievement: %w", err)
	}
	return &entity.Achievement{
		UserID:        a.UserID,
		AchievementID: a.AchievementID,
		UnlockedAtMs:  saved.UnlockedAtMs,
		ReceivedAtMs:  saved.ReceivedAtMs,
		Detail:        saved.Detail,
		ClientIP:      saved.ClientIP,
	}, status == 1, nil
}

// Peek 读取队头至多 n 条(不移除);无法解码的元素计入 count 并通过 malformed 报告。
func (s *AchievementRedisStorage) Peek(ctx context.Context, n int64) (items []*entity.Achievement, count int, malformed int, err error) {
	raw, err := s.peekRaw(ctx, n)
	if err != nil {
		return nil, 0, 0, err
	}
	items = make([]*entity.Achievement, 0, len(raw))
	for _, r := range raw {
		var q queuedAchievement
		if err := json.Unmarshal([]byte(r), &q); err != nil || q.UserID == "" || q.AchievementID == "" {
			malformed++
			continue
		}
		items = append(items, &entity.Achievement{
			UserID: q.UserID, AchievementID: q.AchievementID,
			UnlockedAtMs: q.UnlockedAtMs, ReceivedAtMs: q.ReceivedAtMs, Detail: q.Detail, ClientIP: q.ClientIP,
		})
	}
	return items, len(raw), malformed, nil
}
