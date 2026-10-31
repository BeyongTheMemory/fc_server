package repository_impl

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"fc_server/internal/domain/run/entity"
	"fc_server/internal/domain/run/repository"

	"github.com/redis/go-redis/v9"
)

// 对局票: run:{run_id}  HASH user_id/seed/started_at_ms/expires_at_ms,
// 提交成绩后增加 submitted(成绩指纹)/submitted_at_ms;TTL 为对局票有效期。
const keyPrefixRun = "run:"

func runKey(runID string) string { return keyPrefixRun + runID }

// luaClaimRun 占用唯一的成绩提交名额,返回 missing / new / replay / conflict。
const luaClaimRun = `
if redis.call('EXISTS', KEYS[1]) == 0 then
  return 'missing'
end
local cur = redis.call('HGET', KEYS[1], 'submitted')
if not cur then
  redis.call('HSET', KEYS[1], 'submitted', ARGV[1], 'submitted_at_ms', ARGV[2])
  return 'new'
end
if cur == ARGV[1] then
  return 'replay'
end
return 'conflict'
`

var claimRunScript = redis.NewScript(luaClaimRun)

type RunRedisStorage struct {
	client *redis.Client
}

var _ repository.RunRepository = (*RunRedisStorage)(nil)

func NewRunRedisStorage(client *redis.Client) *RunRedisStorage {
	return &RunRedisStorage{client: client}
}

func (s *RunRedisStorage) Create(ctx context.Context, run *entity.Run, ttl time.Duration) error {
	key := runKey(run.ID)
	_, err := s.client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.HSet(ctx, key,
			"user_id", run.UserID,
			"seed", run.Seed,
			"started_at_ms", run.StartedAtMs,
			"expires_at_ms", run.ExpiresAtMs)
		pipe.PExpire(ctx, key, ttl)
		return nil
	})
	if err != nil {
		return fmt.Errorf("create run: %w", err)
	}
	return nil
}

func (s *RunRedisStorage) Get(ctx context.Context, runID string) (*entity.Run, error) {
	m, err := s.client.HGetAll(ctx, runKey(runID)).Result()
	if err != nil {
		return nil, fmt.Errorf("get run: %w", err)
	}
	if len(m) == 0 {
		return nil, repository.ErrRunNotFound
	}
	started, _ := strconv.ParseInt(m["started_at_ms"], 10, 64)
	expires, _ := strconv.ParseInt(m["expires_at_ms"], 10, 64)
	return &entity.Run{
		ID: runID, UserID: m["user_id"], Seed: m["seed"],
		StartedAtMs: started, ExpiresAtMs: expires,
	}, nil
}

func (s *RunRedisStorage) ClaimSubmission(ctx context.Context, runID, fingerprint string, nowMs int64) (repository.ClaimResult, error) {
	res, err := claimRunScript.Run(ctx, s.client, []string{runKey(runID)}, fingerprint, nowMs).Text()
	if err != nil {
		return 0, fmt.Errorf("claim run: %w", err)
	}
	switch res {
	case "new":
		return repository.ClaimNew, nil
	case "replay":
		return repository.ClaimReplay, nil
	case "conflict":
		return repository.ClaimConflict, nil
	default:
		return 0, repository.ErrRunNotFound
	}
}
