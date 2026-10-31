package repository_impl

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"fc_server/internal/domain/person/entity"
	"fc_server/internal/domain/person/entity/vo"
	"fc_server/internal/domain/person/repository"

	"github.com/redis/go-redis/v9"
)

const (
	replyIdentityMismatch = "IDENTITY_MISMATCH"
	replyPlayerNotFound   = "PLAYER_NOT_FOUND"
)

// luaUpsertOnLogin 创建玩家(默认资料)或仅刷新登录时间;两种情况都递增 revision 并标脏。
// 并发首次登录在 Redis 内串行执行,不会产生两份玩家或覆盖已授权资料。
const luaUpsertOnLogin = `
local key, dirty = KEYS[1], KEYS[2]
local uid, appid, openid, now = ARGV[1], ARGV[2], ARGV[3], ARGV[4]
if redis.call('EXISTS', key) == 1 then
  if redis.call('HGET', key, 'appid') ~= appid or redis.call('HGET', key, 'openid') ~= openid then
    return redis.error_reply('` + replyIdentityMismatch + `')
  end
  redis.call('HSET', key, 'last_login_at', now)
  redis.call('HINCRBY', key, 'revision', 1)
else
  redis.call('HSET', key, 'appid', appid, 'openid', openid, 'nickname', '', 'avatar_url', '',
    'created_at', now, 'last_login_at', now, 'updated_at', now, 'revision', 1)
end
redis.call('HSET', dirty, uid, redis.call('HGET', key, 'revision'))
return redis.call('HGETALL', key)
`

// luaUpdateProfile 资料变化时才写入、递增 revision 并标脏。
const luaUpdateProfile = `
local key, dirty = KEYS[1], KEYS[2]
local uid, nick, avatar, now = ARGV[1], ARGV[2], ARGV[3], ARGV[4]
if redis.call('EXISTS', key) == 0 then
  return redis.error_reply('` + replyPlayerNotFound + `')
end
if redis.call('HGET', key, 'nickname') ~= nick or redis.call('HGET', key, 'avatar_url') ~= avatar then
  redis.call('HSET', key, 'nickname', nick, 'avatar_url', avatar, 'updated_at', now)
  local rev = redis.call('HINCRBY', key, 'revision', 1)
  redis.call('HSET', dirty, uid, rev)
end
return redis.call('HGETALL', key)
`

// luaAckDirty 仅当待同步版本仍是已备份的版本时清除脏标记,
// 备份期间发生的新修改会保留下来等待下一轮。
const luaAckDirty = `
if redis.call('HGET', KEYS[1], ARGV[1]) == ARGV[2] then
  return redis.call('HDEL', KEYS[1], ARGV[1])
end
return 0
`

// luaRestorePlayer 从备份恢复:只在 Redis 中没有同等或更新版本时覆盖,不标脏。
const luaRestorePlayer = `
local key = KEYS[1]
local cur = tonumber(redis.call('HGET', key, 'revision'))
if cur and cur >= tonumber(ARGV[8]) then
  return 0
end
redis.call('HSET', key, 'appid', ARGV[1], 'openid', ARGV[2], 'nickname', ARGV[3], 'avatar_url', ARGV[4],
  'created_at', ARGV[5], 'last_login_at', ARGV[6], 'updated_at', ARGV[7], 'revision', ARGV[8])
return 1
`

var (
	upsertOnLoginScript = redis.NewScript(luaUpsertOnLogin)
	updateProfileScript = redis.NewScript(luaUpdateProfile)
	ackDirtyScript      = redis.NewScript(luaAckDirty)
	restorePlayerScript = redis.NewScript(luaRestorePlayer)
)

type PlayerRedisStorage struct {
	client *redis.Client
}

var _ repository.PlayerRepository = (*PlayerRedisStorage)(nil)

func NewPlayerRedisStorage(client *redis.Client) *PlayerRedisStorage {
	return &PlayerRedisStorage{client: client}
}

func (s *PlayerRedisStorage) UpsertOnLogin(ctx context.Context, userID, appID, openID string, now int64) (*entity.Person, error) {
	fields, err := upsertOnLoginScript.Run(ctx, s.client, []string{playerKey(userID), keyPlayerDirty},
		userID, appID, openID, now).StringSlice()
	if err != nil {
		if strings.Contains(err.Error(), replyIdentityMismatch) {
			return nil, fmt.Errorf("upsert player %s: %w", userID, repository.ErrIdentityMismatch)
		}
		return nil, fmt.Errorf("upsert player: %w", err)
	}
	return parsePerson(userID, pairsToMap(fields)), nil
}

func (s *PlayerRedisStorage) Get(ctx context.Context, userID string) (*entity.Person, error) {
	m, err := s.client.HGetAll(ctx, playerKey(userID)).Result()
	if err != nil {
		return nil, fmt.Errorf("get player: %w", err)
	}
	if len(m) == 0 {
		return nil, repository.ErrPlayerNotFound
	}
	return parsePerson(userID, m), nil
}

func (s *PlayerRedisStorage) GetMany(ctx context.Context, userIDs []string) (map[string]*entity.Person, error) {
	out := make(map[string]*entity.Person, len(userIDs))
	if len(userIDs) == 0 {
		return out, nil
	}
	pipe := s.client.Pipeline()
	cmds := make([]*redis.MapStringStringCmd, len(userIDs))
	for i, id := range userIDs {
		cmds[i] = pipe.HGetAll(ctx, playerKey(id))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("get players: %w", err)
	}
	for i, id := range userIDs {
		if m := cmds[i].Val(); len(m) > 0 {
			out[id] = parsePerson(id, m)
		}
	}
	return out, nil
}

func (s *PlayerRedisStorage) UpdateProfile(ctx context.Context, userID string, profile vo.Profile, now int64) (*entity.Person, error) {
	fields, err := updateProfileScript.Run(ctx, s.client, []string{playerKey(userID), keyPlayerDirty},
		userID, profile.Nickname, profile.AvatarURL, now).StringSlice()
	if err != nil {
		if strings.Contains(err.Error(), replyPlayerNotFound) {
			return nil, repository.ErrPlayerNotFound
		}
		return nil, fmt.Errorf("update profile: %w", err)
	}
	return parsePerson(userID, pairsToMap(fields)), nil
}

// DirtyPlayer 一条待备份记录。
type DirtyPlayer struct {
	UserID   string
	Revision int64
}

// ScanDirty 遍历待备份集合(不移除);cursor 为 0 表示从头开始,返回的 next 为 0 表示遍历结束。
func (s *PlayerRedisStorage) ScanDirty(ctx context.Context, cursor uint64, count int64) ([]DirtyPlayer, uint64, error) {
	kvs, next, err := s.client.HScan(ctx, keyPlayerDirty, cursor, "", count).Result()
	if err != nil {
		return nil, 0, fmt.Errorf("scan player dirty: %w", err)
	}
	out := make([]DirtyPlayer, 0, len(kvs)/2)
	for i := 0; i+1 < len(kvs); i += 2 {
		rev, _ := strconv.ParseInt(kvs[i+1], 10, 64)
		out = append(out, DirtyPlayer{UserID: kvs[i], Revision: rev})
	}
	return out, next, nil
}

// AckDirty 备份 revision 成功后清除脏标记;若期间有更新的修改则保留。
func (s *PlayerRedisStorage) AckDirty(ctx context.Context, userID string, revision int64) error {
	if err := ackDirtyScript.Run(ctx, s.client, []string{keyPlayerDirty},
		userID, strconv.FormatInt(revision, 10)).Err(); err != nil {
		return fmt.Errorf("ack player dirty: %w", err)
	}
	return nil
}

// Restore 用备份快照恢复玩家(保留 revision,不标脏);返回是否写入。
func (s *PlayerRedisStorage) Restore(ctx context.Context, p *entity.Person) (bool, error) {
	n, err := restorePlayerScript.Run(ctx, s.client, []string{playerKey(p.ID)},
		p.AppID, p.OpenID, p.Profile.Nickname, p.Profile.AvatarURL,
		p.CreatedTime, p.LastLoginTime, p.UpdatedTime, p.Revision).Int()
	if err != nil {
		return false, fmt.Errorf("restore player: %w", err)
	}
	return n == 1, nil
}

func pairsToMap(fields []string) map[string]string {
	m := make(map[string]string, len(fields)/2)
	for i := 0; i+1 < len(fields); i += 2 {
		m[fields[i]] = fields[i+1]
	}
	return m
}

func parsePerson(userID string, m map[string]string) *entity.Person {
	parse := func(k string) int64 {
		n, _ := strconv.ParseInt(m[k], 10, 64)
		return n
	}
	return &entity.Person{
		ID:            userID,
		AppID:         m["appid"],
		OpenID:        m["openid"],
		Profile:       vo.Profile{Nickname: m["nickname"], AvatarURL: m["avatar_url"]},
		CreatedTime:   parse("created_at"),
		LastLoginTime: parse("last_login_at"),
		UpdatedTime:   parse("updated_at"),
		Revision:      parse("revision"),
	}
}
