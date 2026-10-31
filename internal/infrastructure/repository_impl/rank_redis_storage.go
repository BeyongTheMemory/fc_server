package repository_impl

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"fc_server/internal/config"
	"fc_server/internal/domain/rank/entity"
	"fc_server/internal/domain/rank/entity/vo"
	"fc_server/internal/domain/rank/repository"

	"github.com/redis/go-redis/v9"
)

// luaUploadBest 原子完成一次成绩上报:
//   - 每个窗口都写全国榜({base}global,天榜)与省/市/区三级地区榜,成员编码见 encodeMember;
//   - 总榜:只升不降(比较成绩编码 skey,见 scoreKey,依次比较关卡、最后一关修为、修为总数);
//     刷新纪录时把旧成员从旧地区榜移除、
//     写入新成员、更新元数据并标脏;未刷新时仅在昵称变化时刷新昵称(并标脏,使 MySQL 侧跟进);
//   - 周榜/月榜:与总榜是否刷新无关,窗口内只升不降;期号变化视为无记录(窗口滚动重置);
//     窗口 key 每次写入 EXPIRE,旧窗口自动过期。
//
// 修为是任意长度的十进制字符串,脚本不把它们转成数字;比较按字节进行,不依赖 Lua 的
// 字符串比较(它受服务器 locale 影响)。
// 板 key 的构造规则与 redis_keys.go 保持一致,由集成测试守护;
// 脚本内动态构造 key,要求 Redis 为单实例(非 cluster)部署。
const luaUploadBest = `
local userKey, dirtyKey, wUserKey, mUserKey = KEYS[1], KEYS[2], KEYS[3], KEYS[4]
local uid, name = ARGV[1], ARGV[2]
local ts = ARGV[6]
local prov, city, dist = ARGV[7], ARGV[8], ARGV[9]
local markDirty = ARGV[10]
local wWin, mWin = ARGV[11], ARGV[12]
local wTTL, mTTL = tonumber(ARGV[13]), tonumber(ARGV[14])
local member = ARGV[15]
local skey = ARGV[16]
local ip = ARGV[17]

local function boardKeys(base, p, c, d)
  local ks = {base .. 'global'}
  if p ~= '' then
    ks[#ks+1] = base .. 'prov:' .. p
    if c ~= '' then
      ks[#ks+1] = base .. 'city:' .. p .. '|' .. c
      if d ~= '' then
        ks[#ks+1] = base .. 'dist:' .. p .. '|' .. c .. '|' .. d
      end
    end
  end
  return ks
end

-- 字节序比较 a > b
local function greater(a, b)
  local n = math.min(#a, #b)
  for i = 1, n do
    local x, y = string.byte(a, i), string.byte(b, i)
    if x ~= y then
      return x > y
    end
  end
  return #a > #b
end

local function better(key)
  local cur = redis.call('HGET', key, 'skey')
  if not cur then
    return true
  end
  return greater(skey, cur)
end

local function replaceEntry(uKey, base, ttl)
  local old = redis.call('HMGET', uKey, 'member', 'prov', 'city', 'dist')
  if old[1] then
    for _, k in ipairs(boardKeys(base, old[2] or '', old[3] or '', old[4] or '')) do
      redis.call('ZREM', k, old[1])
    end
  end
  for _, k in ipairs(boardKeys(base, prov, city, dist)) do
    redis.call('ZADD', k, 0, member)
    if ttl then
      redis.call('EXPIRE', k, ttl)
    end
  end
  redis.call('HSET', uKey, 'name', name, 'stage', ARGV[3], 'scult', ARGV[4], 'tcult', ARGV[5],
    'skey', skey, 'achieved_at', ts, 'member', member, 'prov', prov, 'city', city, 'dist', dist, 'ip', ip)
end

local function updateWindow(uKey, base, win, ttl)
  local curWin = redis.call('HGET', uKey, 'win')
  if curWin == win and not better(uKey) then
    if redis.call('HGET', uKey, 'name') ~= name then
      redis.call('HSET', uKey, 'name', name)
    end
    return
  end
  if curWin ~= win then
    redis.call('DEL', uKey)
  end
  replaceEntry(uKey, base, ttl)
  redis.call('HSET', uKey, 'win', win)
  redis.call('EXPIRE', uKey, ttl)
end

local updated = 0
if better(userKey) then
  replaceEntry(userKey, 'lb:', nil)
  if markDirty == '1' then
    redis.call('SADD', dirtyKey, uid)
  end
  updated = 1
elseif redis.call('HGET', userKey, 'name') ~= name then
  redis.call('HSET', userKey, 'name', name)
  if markDirty == '1' then
    redis.call('SADD', dirtyKey, uid)
  end
end

if wWin ~= '' then
  updateWindow(wUserKey, 'lb:w:' .. wWin .. ':', wWin, wTTL)
end
if mWin ~= '' then
  updateWindow(mUserKey, 'lb:m:' .. mWin .. ':', mWin, mTTL)
end

local best = redis.call('HMGET', userKey, 'stage', 'scult', 'tcult')
return {updated, best[1], best[2], best[3]}
`

var uploadBestScript = redis.NewScript(luaUploadBest)

type RankRedisStorage struct {
	client *redis.Client
}

var (
	rankRedisStorage *RankRedisStorage
	_                repository.RankStorage = (*RankRedisStorage)(nil)
)

func Init(cfg *config.Config) {
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		panic("Failed to connect to Redis for rank: " + err.Error())
	}
	rankRedisStorage = NewRankRedisStorage(client)
}

func NewRankRedisStorage(client *redis.Client) *RankRedisStorage {
	return &RankRedisStorage{client: client}
}

func GetRankRedisStorage() *RankRedisStorage {
	if rankRedisStorage == nil {
		panic("rankRedisStorage is not initialized")
	}
	return rankRedisStorage
}

// Client 暴露底层连接给基础设施层内的其它组件(如 MySQL 同步器)复用。
func (r *RankRedisStorage) Client() *redis.Client {
	return r.client
}

func (r *RankRedisStorage) UploadBest(ctx context.Context, location *vo.Location, user *vo.UserInfo, score vo.Score, clientIP string) (bool, vo.Score, error) {
	now := time.Now()
	return r.uploadBest(ctx, location, user, score, clientIP, now.Unix(), weekWindowId(now), monthWindowId(now), true)
}

// uploadBest 空窗口期号表示跳过对应窗口榜(重建场景仅恢复总榜,R12)。
func (r *RankRedisStorage) uploadBest(ctx context.Context, location *vo.Location, user *vo.UserInfo, score vo.Score, clientIP string, achievedAt int64, wWin, mWin string, markDirty bool) (bool, vo.Score, error) {
	loc := location
	if loc == nil {
		loc = &vo.Location{}
	}
	dirtyFlag := "0"
	if markDirty {
		dirtyFlag = "1"
	}
	res, err := uploadBestScript.Run(ctx, r.client,
		[]string{userKey(user.UserId), keyDirtySet, wUserKey(user.UserId), mUserKey(user.UserId)},
		user.UserId, user.UserName,
		strconv.FormatInt(score.Stage, 10),
		score.StageCultivation.String(),
		score.TotalCultivation.String(),
		achievedAt, loc.Province, loc.City, loc.District, dirtyFlag,
		wWin, mWin, weekTTLSeconds, monthTTLSeconds,
		encodeMember(score, achievedAt, user.UserId),
		scoreKey(score),
		clientIP,
	).Slice()
	if err != nil {
		return false, vo.Score{}, fmt.Errorf("upload best score: %w", err)
	}
	if len(res) != 4 {
		return false, vo.Score{}, fmt.Errorf("upload best score: unexpected script reply %v", res)
	}
	updated, _ := res[0].(int64)
	return updated == 1, vo.Score{
		Stage:            parseInt64(res[1]),
		StageCultivation: parseCultivation(res[2]),
		TotalCultivation: parseCultivation(res[3]),
	}, nil
}

// windowContext 一个时间窗口当期的 key 前缀与元数据 key。
type windowContext struct {
	base    string
	metaKey func(string) string
	winID   string // 月/周当期期号;总榜为空
}

func windowContextAt(window string, now time.Time) windowContext {
	switch window {
	case entity.WindowMonth:
		id := monthWindowId(now)
		return windowContext{base: monthBase(id), metaKey: mUserKey, winID: id}
	case entity.WindowWeek:
		id := weekWindowId(now)
		return windowContext{base: weekBase(id), metaKey: wUserKey, winID: id}
	default:
		return windowContext{base: baseAll, metaKey: userKey}
	}
}

// boardKey 榜单的 Redis key 与展示地址;地区路径不全时 key 为空。
func boardKey(ref repository.BoardRef, wc windowContext) (string, string) {
	loc := ref.Location
	if loc == nil {
		loc = &vo.Location{}
	}
	switch ref.Level {
	case vo.Nation:
		return globalKeyIn(wc.base), ""
	case vo.Province:
		return provKeyIn(wc.base, loc.Province), loc.Province
	case vo.City:
		return cityKeyIn(wc.base, loc.Province, loc.City), loc.City
	case vo.District:
		return distKeyIn(wc.base, loc.Province, loc.City, loc.District), loc.District
	default:
		return "", ""
	}
}

func (r *RankRedisStorage) CountMembers(ctx context.Context, refs []repository.BoardRef) ([]int64, error) {
	now := time.Now()
	counts := make([]int64, len(refs))
	pipe := r.client.Pipeline()
	cmds := make([]*redis.IntCmd, len(refs))
	for i, ref := range refs {
		if key, _ := boardKey(ref, windowContextAt(ref.Window, now)); key != "" {
			cmds[i] = pipe.ZCard(ctx, key)
		}
	}
	if pipe.Len() == 0 {
		return counts, nil
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("count board members: %w", err)
	}
	for i, cmd := range cmds {
		if cmd != nil {
			counts[i] = cmd.Val()
		}
	}
	return counts, nil
}

// selfMembers 读取玩家在各窗口当期的榜单成员串(用于 ZREVRANK);当期无成绩为空串。
func (r *RankRedisStorage) selfMembers(ctx context.Context, userId string, contexts map[string]windowContext) (map[string]string, error) {
	out := make(map[string]string, len(contexts))
	pipe := r.client.Pipeline()
	cmds := make(map[string]*redis.SliceCmd, len(contexts))
	for w, wc := range contexts {
		cmds[w] = pipe.HMGet(ctx, wc.metaKey(userId), "member", "win")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("fetch self members: %w", err)
	}
	for w, cmd := range cmds {
		vals := cmd.Val()
		if len(vals) != 2 || vals[0] == nil {
			continue
		}
		if wc := contexts[w]; wc.winID != "" && vals[1] != wc.winID {
			continue
		}
		out[w], _ = vals[0].(string)
	}
	return out, nil
}

func (r *RankRedisStorage) GetBoards(ctx context.Context, refs []repository.BoardRef, userId string, limit int) ([]*entity.RankResult, error) {
	now := time.Now()
	contexts := map[string]windowContext{}
	for _, ref := range refs {
		contexts[ref.Window] = windowContextAt(ref.Window, now)
	}
	self, err := r.selfMembers(ctx, userId, contexts)
	if err != nil {
		return nil, err
	}

	type boardCmds struct {
		wc       windowContext
		key      string
		address  string
		entries  *redis.StringSliceCmd
		selfRank *redis.IntCmd
	}
	pipe := r.client.Pipeline()
	cmds := make([]boardCmds, len(refs))
	for i, ref := range refs {
		bc := boardCmds{wc: contexts[ref.Window]}
		bc.key, bc.address = boardKey(ref, bc.wc)
		if bc.key != "" {
			bc.entries = pipe.ZRevRange(ctx, bc.key, 0, int64(limit-1))
			if m := self[ref.Window]; m != "" {
				bc.selfRank = pipe.ZRevRank(ctx, bc.key, m)
			}
		}
		cmds[i] = bc
	}
	if pipe.Len() > 0 {
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return nil, fmt.Errorf("fetch boards: %w", err)
		}
	}

	// 昵称来自各窗口元数据(无玩家资料的历史成员才会用到),同一 key 只读一次
	names := map[string]string{}
	var metaKeys []string
	for _, bc := range cmds {
		if bc.entries == nil {
			continue
		}
		for _, m := range bc.entries.Val() {
			if _, _, id, ok := decodeMember(m); ok {
				mk := bc.wc.metaKey(id)
				if _, seen := names[mk]; !seen {
					names[mk] = ""
					metaKeys = append(metaKeys, mk)
				}
			}
		}
	}
	if len(metaKeys) > 0 {
		metaPipe := r.client.Pipeline()
		nameCmds := make([]*redis.StringCmd, len(metaKeys))
		for i, mk := range metaKeys {
			nameCmds[i] = metaPipe.HGet(ctx, mk, "name")
		}
		if _, err := metaPipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return nil, fmt.Errorf("fetch user names: %w", err)
		}
		for i, mk := range metaKeys {
			names[mk] = nameCmds[i].Val()
		}
	}

	results := make([]*entity.RankResult, 0, len(refs))
	for i, bc := range cmds {
		board := entity.NewRankResult(bc.address, refs[i].Level)
		board.Window = refs[i].Window
		if bc.entries != nil {
			for _, m := range bc.entries.Val() {
				score, at, id, ok := decodeMember(m)
				if !ok {
					continue
				}
				board.ScoreMetaList = append(board.ScoreMetaList, &vo.ScoreMeta{
					UserInfo:   &vo.UserInfo{UserId: id, UserName: names[bc.wc.metaKey(id)]},
					Score:      score,
					CreateTime: at,
				})
			}
			if bc.selfRank != nil {
				if rank, err := bc.selfRank.Result(); err == nil {
					board.SelfRank = rank + 1
					board.SelfScore, _, _, _ = decodeMember(self[refs[i].Window])
				}
			}
		}
		results = append(results, board)
	}
	return results, nil
}

func (r *RankRedisStorage) GetMemberBests(ctx context.Context, window string, userIds []string) ([]*vo.ScoreMeta, error) {
	out := make([]*vo.ScoreMeta, 0, len(userIds))
	if len(userIds) == 0 {
		return out, nil
	}
	wc := windowContextAt(window, time.Now())
	pipe := r.client.Pipeline()
	cmds := make([]*redis.SliceCmd, len(userIds))
	for i, id := range userIds {
		cmds[i] = pipe.HMGet(ctx, wc.metaKey(id), "stage", "scult", "tcult", "achieved_at", "name", "win")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("fetch member bests: %w", err)
	}
	for i, id := range userIds {
		vals := cmds[i].Val()
		if len(vals) != 6 || vals[0] == nil {
			continue
		}
		if wc.winID != "" && vals[5] != wc.winID {
			continue // 上一期的成绩不算当期
		}
		name, _ := vals[4].(string)
		out = append(out, &vo.ScoreMeta{
			UserInfo: &vo.UserInfo{UserId: id, UserName: name},
			Score: vo.Score{
				Stage:            parseInt64(vals[0]),
				StageCultivation: parseCultivation(vals[1]),
				TotalCultivation: parseCultivation(vals[2]),
			},
			CreateTime: parseInt64(vals[3]),
		})
	}
	return out, nil
}

func parseInt64(v interface{}) int64 {
	s, _ := v.(string)
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

// parseCultivation 读取脚本写入的修为字符串;缺失或损坏时视为 0。
func parseCultivation(v interface{}) vo.Cultivation {
	s, _ := v.(string)
	c, err := vo.ParseCultivation(s)
	if err != nil {
		return "0"
	}
	return c
}

// UserBest 玩家历史最好成绩元数据快照,供 MySQL 同步与重建使用(仅总榜)。
type UserBest struct {
	UserId     string
	UserName   string
	Score      vo.Score
	AchievedAt int64
	ClientIP   string // 创下该成绩的上报来源 IP
	Province   string
	City       string
	District   string
}

// PopDirty 原子取走至多 n 个待同步玩家 ID;脏集为空时返回空切片。
func (r *RankRedisStorage) PopDirty(ctx context.Context, n int64) ([]string, error) {
	ids, err := r.client.SPopN(ctx, keyDirtySet, n).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("pop dirty set: %w", err)
	}
	return ids, nil
}

// MarkDirty 将玩家 ID 放回脏集(同步失败时回补,重试安全)。
func (r *RankRedisStorage) MarkDirty(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	members := make([]interface{}, len(ids))
	for i, id := range ids {
		members[i] = id
	}
	if err := r.client.SAdd(ctx, keyDirtySet, members...).Err(); err != nil {
		return fmt.Errorf("mark dirty: %w", err)
	}
	return nil
}

// GetUserBest 读取玩家总榜最好成绩元数据;不存在时 ok=false。
func (r *RankRedisStorage) GetUserBest(ctx context.Context, userId string) (*UserBest, bool, error) {
	m, err := r.client.HGetAll(ctx, userKey(userId)).Result()
	if err != nil {
		return nil, false, fmt.Errorf("get user best %s: %w", userId, err)
	}
	if len(m) == 0 || m["stage"] == "" {
		return nil, false, nil
	}
	return &UserBest{
		UserId:   userId,
		UserName: m["name"],
		Score: vo.Score{
			Stage:            parseInt64(m["stage"]),
			StageCultivation: parseCultivation(m["scult"]),
			TotalCultivation: parseCultivation(m["tcult"]),
		},
		AchievedAt: parseInt64(m["achieved_at"]),
		ClientIP:   m["ip"],
		Province:   m["prov"],
		City:       m["city"],
		District:   m["dist"],
	}, true, nil
}

// RestoreBest 用于从 MySQL 重建:保留原达成时间、不标脏、不生成窗口榜
// (周/月榜不持久化,R12),幂等只升不降。
func (r *RankRedisStorage) RestoreBest(ctx context.Context, ub *UserBest) error {
	loc := &vo.Location{Province: ub.Province, City: ub.City, District: ub.District}
	user := &vo.UserInfo{UserId: ub.UserId, UserName: ub.UserName}
	_, _, err := r.uploadBest(ctx, loc, user, ub.Score, ub.ClientIP, ub.AchievedAt, "", "", false)
	return err
}
