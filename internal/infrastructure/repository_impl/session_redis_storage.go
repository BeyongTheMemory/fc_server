package repository_impl

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"fc_server/internal/domain/person/entity"
	"fc_server/internal/domain/person/repository"

	"github.com/redis/go-redis/v9"
)

type SessionRedisStorage struct {
	client *redis.Client
}

var _ repository.SessionRepository = (*SessionRedisStorage)(nil)

func NewSessionRedisStorage(client *redis.Client) *SessionRedisStorage {
	return &SessionRedisStorage{client: client}
}

func (s *SessionRedisStorage) Create(ctx context.Context, tokenHash string, session *entity.Session, ttl time.Duration) error {
	key := sessionKey(tokenHash)
	_, err := s.client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.HSet(ctx, key,
			"user_id", session.UserID,
			"appid", session.AppID,
			"issued_at", session.IssuedAt,
			"expires_at", session.ExpiresAt)
		pipe.Expire(ctx, key, ttl)
		return nil
	})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

func (s *SessionRedisStorage) Get(ctx context.Context, tokenHash string) (*entity.Session, error) {
	m, err := s.client.HGetAll(ctx, sessionKey(tokenHash)).Result()
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	if len(m) == 0 {
		return nil, repository.ErrSessionNotFound
	}
	issued, _ := strconv.ParseInt(m["issued_at"], 10, 64)
	expires, _ := strconv.ParseInt(m["expires_at"], 10, 64)
	return &entity.Session{UserID: m["user_id"], AppID: m["appid"], IssuedAt: issued, ExpiresAt: expires}, nil
}

func (s *SessionRedisStorage) Delete(ctx context.Context, tokenHash string) error {
	if err := s.client.Del(ctx, sessionKey(tokenHash)).Err(); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// luaRateLimit 计数并在窗口首次计数时设置过期,保证限流键不会永久残留。
const luaRateLimit = `
local n = redis.call('INCR', KEYS[1])
if n == 1 or redis.call('PTTL', KEYS[1]) < 0 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
return n
`

var rateLimitScript = redis.NewScript(luaRateLimit)

type RateLimiterRedis struct {
	client *redis.Client
}

var _ repository.AttemptLimiter = (*RateLimiterRedis)(nil)

func NewRateLimiterRedis(client *redis.Client) *RateLimiterRedis {
	return &RateLimiterRedis{client: client}
}

func (l *RateLimiterRedis) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
	n, err := rateLimitScript.Run(ctx, l.client, []string{rateLimitKey(key)}, window.Milliseconds()).Int()
	if err != nil {
		return false, fmt.Errorf("rate limit: %w", err)
	}
	return n <= limit, nil
}
