package repository_impl

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// lockedListQueue Redis LIST 实现的待持久化队列:生产者 RPUSH,后台任务持锁从队头批量读取、
// 写入 MySQL 成功后再删除队头。游戏记录与成就共用这一机制,各自使用独立的 key。
type lockedListQueue struct {
	client  *redis.Client
	key     string
	lockKey string
	maxLen  int
	name    string // 出现在错误信息中
}

// luaAppendCapped 积压未达上限时追加,否则拒绝,防止 MySQL 长期不可用时撑爆 Redis 内存。
const luaAppendCapped = `
if redis.call('LLEN', KEYS[1]) >= tonumber(ARGV[2]) then
  return 0
end
redis.call('RPUSH', KEYS[1], ARGV[1])
return 1
`

// luaTrimIfLocked 只有仍持有消费锁时才删除已持久化的队头元素。
// 锁过期后另一实例可能已开始消费,此时放弃删除:那批数据会被重新读取,
// 由 MySQL 唯一键去重,不会丢失也不会多删。
const luaTrimIfLocked = `
if redis.call('GET', KEYS[2]) ~= ARGV[1] then
  return 0
end
redis.call('LTRIM', KEYS[1], tonumber(ARGV[2]), -1)
return 1
`

const luaReleaseLock = `
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`

var (
	appendCappedScript = redis.NewScript(luaAppendCapped)
	trimIfLockedScript = redis.NewScript(luaTrimIfLocked)
	releaseLockScript  = redis.NewScript(luaReleaseLock)
)

// appendCapped 追加一条;积压已满返回 false。
func (q *lockedListQueue) appendCapped(ctx context.Context, payload string) (bool, error) {
	ok, err := appendCappedScript.Run(ctx, q.client, []string{q.key}, payload, q.maxLen).Int()
	if err != nil {
		return false, fmt.Errorf("append %s: %w", q.name, err)
	}
	return ok == 1, nil
}

// AcquireFlushLock 尝试获取消费锁;token 用于之后的 TrimIfLocked/ReleaseFlushLock。
func (q *lockedListQueue) AcquireFlushLock(ctx context.Context, token string, ttl time.Duration) (bool, error) {
	ok, err := q.client.SetNX(ctx, q.lockKey, token, ttl).Result()
	if err != nil {
		return false, fmt.Errorf("acquire %s flush lock: %w", q.name, err)
	}
	return ok, nil
}

func (q *lockedListQueue) ReleaseFlushLock(ctx context.Context, token string) error {
	if err := releaseLockScript.Run(ctx, q.client, []string{q.lockKey}, token).Err(); err != nil {
		return fmt.Errorf("release %s flush lock: %w", q.name, err)
	}
	return nil
}

// peekRaw 读取队头至多 n 条原始元素(不移除)。
func (q *lockedListQueue) peekRaw(ctx context.Context, n int64) ([]string, error) {
	raw, err := q.client.LRange(ctx, q.key, 0, n-1).Result()
	if err != nil {
		return nil, fmt.Errorf("peek %s: %w", q.name, err)
	}
	return raw, nil
}

// TrimIfLocked 删除队头 n 条;未持有锁时不删除并返回 false。
func (q *lockedListQueue) TrimIfLocked(ctx context.Context, token string, n int) (bool, error) {
	ok, err := trimIfLockedScript.Run(ctx, q.client, []string{q.key, q.lockKey}, token, n).Int()
	if err != nil {
		return false, fmt.Errorf("trim %s: %w", q.name, err)
	}
	return ok == 1, nil
}

// Len 当前积压数量。
func (q *lockedListQueue) Len(ctx context.Context) (int64, error) {
	n, err := q.client.LLen(ctx, q.key).Result()
	if err != nil {
		return 0, fmt.Errorf("%s queue length: %w", q.name, err)
	}
	return n, nil
}
