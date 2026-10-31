package repository_impl

import (
	"context"
	"strings"
	"testing"
	"time"

	"fc_server/internal/domain/rank/entity"
	"fc_server/internal/domain/rank/entity/vo"
	"fc_server/internal/domain/rank/repository"

	"github.com/redis/go-redis/v9"
)

// 集成测试:连接本地 Redis 的 DB 15(与线上 DB 0 隔离),不可达时跳过。
func newTestStorage(t *testing.T) *RankRedisStorage {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379", DB: 15})
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("redis not available at 127.0.0.1:6379: %v", err)
	}
	if err := client.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("flush test db: %v", err)
	}
	t.Cleanup(func() {
		_ = client.FlushDB(context.Background()).Err()
		_ = client.Close()
	})
	return NewRankRedisStorage(client)
}

var (
	locFull = &vo.Location{Province: "浙江省", City: "杭州市", District: "西湖区"}
	userA   = &vo.UserInfo{UserId: "u_a", UserName: "阿甘"}
)

func sc(stage, last, total int64) vo.Score {
	return vo.Score{Stage: stage, StageCultivation: vo.CultivationFromInt(last), TotalCultivation: vo.CultivationFromInt(total)}
}

// big 由十进制字符串构造任意大小的成绩。
func big(stage int64, last, total string) vo.Score {
	return vo.Score{Stage: stage, StageCultivation: vo.Cultivation(last), TotalCultivation: vo.Cultivation(total)}
}

func mustUpload(t *testing.T, s *RankRedisStorage, loc *vo.Location, u *vo.UserInfo, score vo.Score) (bool, vo.Score) {
	t.Helper()
	updated, best, err := s.UploadBest(context.Background(), loc, u, score, "203.0.113.9")
	if err != nil {
		t.Fatalf("UploadBest(%s,%+v): %v", u.UserId, score, err)
	}
	return updated, best
}

func membersOf(t *testing.T, s *RankRedisStorage, key string) []string {
	t.Helper()
	ids := []string{}
	for _, m := range s.Client().ZRevRange(context.Background(), key, 0, -1).Val() {
		if _, _, id, ok := decodeMember(m); ok {
			ids = append(ids, id)
		}
	}
	return ids
}

func allRefs(level vo.AddressLevel, loc *vo.Location) []repository.BoardRef {
	refs := make([]repository.BoardRef, 0, 3)
	for _, w := range entity.Windows {
		refs = append(refs, repository.BoardRef{Window: w, Level: level, Location: loc})
	}
	return refs
}

func TestUploadBestOnlyImproves(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()

	steps := []struct {
		name        string
		score       vo.Score
		wantUpdated bool
		wantBest    vo.Score
	}{
		{"first", sc(5, 100, 1000), true, sc(5, 100, 1000)},
		{"lower total", sc(5, 100, 900), false, sc(5, 100, 1000)},
		{"equal (idempotent replay)", sc(5, 100, 1000), false, sc(5, 100, 1000)},
		{"higher last-stage beats lower total", sc(5, 101, 0), true, sc(5, 101, 0)},
		{"lower stage loses despite huge cultivation", sc(4, 999999, 999999), false, sc(5, 101, 0)},
		{"higher stage wins", sc(6, 0, 0), true, sc(6, 0, 0)},
	}
	for _, st := range steps {
		updated, best := mustUpload(t, s, locFull, userA, st.score)
		if updated != st.wantUpdated || best != st.wantBest {
			t.Fatalf("%s: got (%v,%+v), want (%v,%+v)", st.name, updated, best, st.wantUpdated, st.wantBest)
		}
	}

	// 全国榜与三级地区榜各只有一条(不因多次上传膨胀),成员编码为最新最好成绩
	for _, key := range []string{"lb:global", "lb:prov:浙江省", "lb:city:浙江省|杭州市", "lb:dist:浙江省|杭州市|西湖区"} {
		ms := s.Client().ZRange(ctx, key, 0, -1).Val()
		if len(ms) != 1 {
			t.Fatalf("%s members = %v, want 1", key, ms)
		}
		if got, _, id, _ := decodeMember(ms[0]); id != "u_a" || got != sc(6, 0, 0) {
			t.Errorf("%s member = %+v/%s", key, got, id)
		}
	}
	if !s.Client().SIsMember(ctx, "lb:dirty", "u_a").Val() {
		t.Error("dirty set should contain u_a")
	}
	ub, ok, err := s.GetUserBest(ctx, "u_a")
	if err != nil || !ok || ub.Score != sc(6, 0, 0) || ub.UserName != "阿甘" || ub.Province != "浙江省" || ub.AchievedAt == 0 ||
		ub.ClientIP != "203.0.113.9" {
		t.Errorf("GetUserBest = (%+v, %v, %v)", ub, ok, err)
	}
}

func TestUploadBestHugeValuesStayExact(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()
	// 远超 int64 与 2^53 的修为(大罗无尽模式)
	huge := big(MaxEncodableStage, "17976931348623157"+strings.Repeat("0", 292), "9"+strings.Repeat("9", 318))
	if _, best := mustUpload(t, s, locFull, userA, huge); best != huge {
		t.Fatalf("best = %+v, want exact %+v", best, huge)
	}
	ub, _, _ := s.GetUserBest(ctx, "u_a")
	if ub.Score != huge {
		t.Errorf("stored = %+v", ub.Score)
	}
	// 末位只大 1 也能比较出来
	better := huge
	better.StageCultivation = vo.Cultivation("17976931348623157" + strings.Repeat("0", 291) + "1")
	if updated, _ := mustUpload(t, s, locFull, userA, better); !updated {
		t.Error("an improvement in the last digit must be detected")
	}
	// 位数更少的数值更小,即便首位数字更大
	if updated, _ := mustUpload(t, s, locFull, userA, big(MaxEncodableStage, "9"+strings.Repeat("9", 307), "1")); updated {
		t.Error("a value with fewer digits must not beat a longer one")
	}
}

func TestBoardOrderAcrossDigitLengths(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()
	players := []struct {
		id    string
		score vo.Score
	}{
		{"u_9digits", big(38, "999999999", "999999999")},
		{"u_21digits", big(38, "100000000000000000000", "100000000000000000000")},
		{"u_10digits", big(38, "1000000000", "1000000000")},
		{"u_30digits_lower_total", big(38, "100000000000000000000000000000", "100000000000000000000000000000")},
		{"u_30digits_higher_total", big(38, "100000000000000000000000000000", "100000000000000000000000000001")},
	}
	for _, p := range players {
		mustUpload(t, s, locFull, &vo.UserInfo{UserId: p.id}, p.score)
	}
	boards, err := s.GetBoards(ctx, allRefs(vo.Nation, nil)[:1], "u_10digits", 10)
	if err != nil {
		t.Fatalf("GetBoards: %v", err)
	}
	want := []string{"u_30digits_higher_total", "u_30digits_lower_total", "u_21digits", "u_10digits", "u_9digits"}
	for i, e := range boards[0].ScoreMetaList {
		if e.UserInfo.UserId != want[i] {
			t.Errorf("pos %d = %s, want %s", i, e.UserInfo.UserId, want[i])
		}
	}
	if boards[0].SelfRank != 4 || boards[0].SelfScore != big(38, "1000000000", "1000000000") {
		t.Errorf("self = %d %+v", boards[0].SelfRank, boards[0].SelfScore)
	}
}

func TestRegionMoveRemovesOldEntries(t *testing.T) {
	s := newTestStorage(t)
	mustUpload(t, s, locFull, userA, sc(1, 0, 0))
	mustUpload(t, s, &vo.Location{Province: "上海市", City: "上海市", District: "浦东新区"}, userA, sc(2, 0, 0))

	if ids := membersOf(t, s, "lb:prov:浙江省"); len(ids) != 0 {
		t.Errorf("old province board = %v, want empty", ids)
	}
	if ids := membersOf(t, s, "lb:prov:上海市"); len(ids) != 1 {
		t.Errorf("new province board = %v", ids)
	}
	if n := s.Client().ZCard(context.Background(), "lb:global").Val(); n != 1 {
		t.Errorf("global board card = %d, want 1", n)
	}
}

func TestPartialLocationJoinsGlobalAndKnownLevels(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()
	mustUpload(t, s, &vo.Location{Province: "浙江省"}, userA, sc(1, 0, 0))
	mustUpload(t, s, &vo.Location{}, &vo.UserInfo{UserId: "u_nowhere"}, sc(1, 0, 0))

	if n := s.Client().ZCard(ctx, "lb:global").Val(); n != 2 {
		t.Errorf("global card = %d, want 2 (players without location still rank nationally)", n)
	}
	if keys := s.Client().Keys(ctx, "lb:city:*").Val(); len(keys) != 0 {
		t.Errorf("no city board expected, got %v", keys)
	}
}

func TestGetBoardsOrderSelfRankAndLimit(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()
	mustUpload(t, s, locFull, &vo.UserInfo{UserId: "u_1", UserName: "one"}, sc(9, 10, 10))
	mustUpload(t, s, locFull, &vo.UserInfo{UserId: "u_2", UserName: "two"}, sc(9, 5, 99))
	mustUpload(t, s, &vo.Location{Province: "浙江省", City: "宁波市"}, &vo.UserInfo{UserId: "u_3", UserName: "three"}, sc(3, 0, 0))

	refs := []repository.BoardRef{
		{Window: entity.WindowAll, Level: vo.Nation},
		{Window: entity.WindowAll, Level: vo.Province, Location: locFull},
		{Window: entity.WindowAll, Level: vo.City, Location: locFull},
		{Window: entity.WindowWeek, Level: vo.Nation},
	}
	boards, err := s.GetBoards(ctx, refs, "u_2", 100)
	if err != nil || len(boards) != 4 {
		t.Fatalf("GetBoards = (%d boards, %v)", len(boards), err)
	}
	global := boards[0]
	if global.Scope != entity.ScopeGlobal || global.Window != entity.WindowAll || len(global.ScoreMetaList) != 3 {
		t.Fatalf("global board = %+v", global)
	}
	if e := global.ScoreMetaList[0]; e.UserInfo.UserId != "u_1" || e.UserInfo.UserName != "one" || e.Score != sc(9, 10, 10) || e.CreateTime == 0 {
		t.Errorf("top entry = %+v / %+v", e, e.UserInfo)
	}
	if global.SelfRank != 2 || global.SelfScore != sc(9, 5, 99) {
		t.Errorf("self = %d %+v, want 2 (9,5,99)", global.SelfRank, global.SelfScore)
	}
	if p := boards[1]; p.Scope != entity.ScopeProvince || p.Address != "浙江省" || len(p.ScoreMetaList) != 3 {
		t.Errorf("province board = %+v", p)
	}
	if c := boards[2]; c.Scope != entity.ScopeCity || c.Address != "杭州市" || len(c.ScoreMetaList) != 2 || c.SelfRank != 2 {
		t.Errorf("city board = %+v", c)
	}
	if w := boards[3]; w.Window != entity.WindowWeek || len(w.ScoreMetaList) != 3 || w.SelfRank != 2 {
		t.Errorf("week global board = %+v", w)
	}

	// limit 截断,但自己的名次仍然可见(FR-005)
	boards, err = s.GetBoards(ctx, refs[:1], "u_3", 2)
	if err != nil {
		t.Fatalf("GetBoards limit=2: %v", err)
	}
	if len(boards[0].ScoreMetaList) != 2 || boards[0].SelfRank != 3 || boards[0].SelfScore != sc(3, 0, 0) {
		t.Errorf("limited board = %d entries, self %d %+v", len(boards[0].ScoreMetaList), boards[0].SelfRank, boards[0].SelfScore)
	}
}

func TestEqualScoreEarlierAchieverRanksFirst(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()
	for _, u := range []struct {
		id string
		at int64
	}{{"u_late", 2000}, {"u_early", 1000}} {
		if _, _, err := s.uploadBest(ctx, locFull, &vo.UserInfo{UserId: u.id}, sc(5, 5, 5), "", u.at, "", "", false); err != nil {
			t.Fatalf("uploadBest: %v", err)
		}
	}
	if ids := membersOf(t, s, "lb:global"); len(ids) != 2 || ids[0] != "u_early" {
		t.Errorf("order = %v, want u_early first", ids)
	}
}

func TestGetBoardsUnknownUserAndIncompleteRegion(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()
	mustUpload(t, s, locFull, userA, sc(1, 1, 1))

	refs := []repository.BoardRef{
		{Window: entity.WindowAll, Level: vo.City, Location: &vo.Location{Province: "浙江省"}},
		{Window: entity.WindowAll, Level: vo.Nation},
	}
	boards, err := s.GetBoards(ctx, refs, "u_ghost", 100)
	if err != nil || len(boards) != 2 {
		t.Fatalf("GetBoards = (%v, %v)", boards, err)
	}
	if len(boards[0].ScoreMetaList) != 0 {
		t.Errorf("incomplete city path should give an empty board: %+v", boards[0])
	}
	if boards[1].SelfRank != 0 || !boards[1].SelfScore.IsZero() || len(boards[1].ScoreMetaList) != 1 {
		t.Errorf("unknown user on global = %+v", boards[1])
	}
}

func TestCountMembers(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()
	mustUpload(t, s, locFull, &vo.UserInfo{UserId: "u_1"}, sc(1, 0, 0))
	mustUpload(t, s, locFull, &vo.UserInfo{UserId: "u_2"}, sc(1, 0, 0))
	mustUpload(t, s, &vo.Location{Province: "浙江省", City: "宁波市"}, &vo.UserInfo{UserId: "u_3"}, sc(1, 0, 0))

	counts, err := s.CountMembers(ctx, []repository.BoardRef{
		{Window: entity.WindowAll, Level: vo.City, Location: locFull},
		{Window: entity.WindowWeek, Level: vo.City, Location: locFull},
		{Window: entity.WindowAll, Level: vo.Province, Location: locFull},
		{Window: entity.WindowAll, Level: vo.City, Location: &vo.Location{Province: "浙江省"}},
		{Window: entity.WindowAll, Level: vo.City, Location: &vo.Location{Province: "无人省", City: "无人市"}},
	})
	if err != nil {
		t.Fatalf("CountMembers: %v", err)
	}
	want := []int64{2, 2, 3, 0, 0}
	for i := range want {
		if counts[i] != want[i] {
			t.Errorf("counts = %v, want %v", counts, want)
			break
		}
	}
}

func TestGetMemberBests(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()
	mustUpload(t, s, locFull, &vo.UserInfo{UserId: "u_1", UserName: "one"}, sc(3, 2, 1))
	mustUpload(t, s, locFull, &vo.UserInfo{UserId: "u_2", UserName: "two"}, sc(4, 0, 0))
	// u_old 只有很久以前那一周的成绩
	if _, _, err := s.uploadBest(ctx, locFull, &vo.UserInfo{UserId: "u_old"}, sc(9, 9, 9), "", 1_600_000_000, "2020-W01", "2020-01", false); err != nil {
		t.Fatalf("seed old window: %v", err)
	}

	all, err := s.GetMemberBests(ctx, entity.WindowAll, []string{"u_1", "u_2", "u_old", "u_none"})
	if err != nil || len(all) != 3 {
		t.Fatalf("all-time bests = (%d, %v), want 3", len(all), err)
	}
	byID := map[string]*vo.ScoreMeta{}
	for _, m := range all {
		byID[m.UserInfo.UserId] = m
	}
	if byID["u_1"].Score != sc(3, 2, 1) || byID["u_1"].UserInfo.UserName != "one" || byID["u_1"].CreateTime == 0 {
		t.Errorf("u_1 = %+v", byID["u_1"])
	}

	week, err := s.GetMemberBests(ctx, entity.WindowWeek, []string{"u_1", "u_2", "u_old"})
	if err != nil || len(week) != 2 {
		t.Fatalf("week bests = (%d, %v), want 2 (stale window excluded)", len(week), err)
	}
	for _, m := range week {
		if m.UserInfo.UserId == "u_old" {
			t.Error("a previous week's score must not count for the current week")
		}
	}
}

func TestDirtySetPopMarkRoundtrip(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()
	mustUpload(t, s, locFull, &vo.UserInfo{UserId: "u_1"}, sc(1, 0, 0))
	mustUpload(t, s, locFull, &vo.UserInfo{UserId: "u_2"}, sc(2, 0, 0))

	ids, err := s.PopDirty(ctx, 10)
	if err != nil || len(ids) != 2 {
		t.Fatalf("PopDirty = %v (err %v), want 2 ids", ids, err)
	}
	if again, _ := s.PopDirty(ctx, 10); len(again) != 0 {
		t.Errorf("second PopDirty = %v, want empty", again)
	}
	if err := s.MarkDirty(ctx, ids); err != nil {
		t.Fatalf("MarkDirty: %v", err)
	}
	if back, _ := s.PopDirty(ctx, 10); len(back) != 2 {
		t.Errorf("after MarkDirty, PopDirty = %v, want 2 ids", back)
	}
}

func TestRestoreBestKeepsTimeSkipsDirtyAndWindows(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()
	ub := &UserBest{UserId: "u_r", UserName: "恢复者", Score: sc(7, 70, 700), AchievedAt: 1700000000,
		Province: "浙江省", City: "杭州市", District: "西湖区"}
	for i := 0; i < 2; i++ { // 重放幂等
		if err := s.RestoreBest(ctx, ub); err != nil {
			t.Fatalf("RestoreBest: %v", err)
		}
	}
	got, ok, err := s.GetUserBest(ctx, "u_r")
	if err != nil || !ok || got.Score != ub.Score || got.AchievedAt != 1700000000 {
		t.Fatalf("restored = (%+v, %v, %v)", got, ok, err)
	}
	if ids := membersOf(t, s, "lb:global"); len(ids) != 1 {
		t.Errorf("global after replay = %v, want one entry", ids)
	}
	if n := s.Client().SCard(ctx, "lb:dirty").Val(); n != 0 {
		t.Errorf("restore must not mark dirty, card = %d", n)
	}
	if keys := s.Client().Keys(ctx, "lb:w:*").Val(); len(keys) != 0 {
		t.Errorf("restore must not create window boards: %v", keys)
	}
}

func TestNameRefreshesOnNonImprovingUpload(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()
	mustUpload(t, s, locFull, &vo.UserInfo{UserId: "u_a", UserName: "阿甘"}, sc(5, 0, 0))
	_, _ = s.PopDirty(ctx, 10)
	before, _, _ := s.GetUserBest(ctx, "u_a")

	if updated, _ := mustUpload(t, s, locFull, &vo.UserInfo{UserId: "u_a", UserName: "新阿甘"}, sc(1, 0, 0)); updated {
		t.Fatal("worse score must not update")
	}
	after, _, _ := s.GetUserBest(ctx, "u_a")
	if after.UserName != "新阿甘" || after.Score != before.Score || after.AchievedAt != before.AchievedAt {
		t.Errorf("before=%+v after=%+v", before, after)
	}
	if !s.Client().SIsMember(ctx, "lb:dirty", "u_a").Val() {
		t.Error("name change should mark player dirty for MySQL sync")
	}
	_, _ = s.PopDirty(ctx, 10)
	mustUpload(t, s, locFull, &vo.UserInfo{UserId: "u_a", UserName: "新阿甘"}, sc(1, 0, 0))
	if n := s.Client().SCard(ctx, "lb:dirty").Val(); n != 0 {
		t.Errorf("same-name non-improving upload must not mark dirty, card = %d", n)
	}
}

func TestWindowKeysHaveTTL(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()
	mustUpload(t, s, locFull, userA, sc(1, 0, 0))
	now := time.Now()
	for _, key := range []string{
		globalKeyIn(weekBase(weekWindowId(now))),
		provKeyIn(monthBase(monthWindowId(now)), "浙江省"),
		wUserKey("u_a"),
	} {
		if ttl := s.Client().TTL(ctx, key).Val(); ttl <= 0 {
			t.Errorf("%s TTL = %v, want > 0", key, ttl)
		}
	}
	for _, key := range []string{"lb:global", "lb:prov:浙江省", "lb:user:u_a"} {
		if ttl := s.Client().TTL(ctx, key).Val(); ttl > 0 {
			t.Errorf("all-time key %s must not expire, TTL = %v", key, ttl)
		}
	}
}

func TestWindowBestIndependentOfAllTime(t *testing.T) {
	// 历史最好 (10,0,0),新一周上报 (5,0,0) → 总榜不变,本周榜 (5,0,0)
	s := newTestStorage(t)
	ctx := context.Background()
	up := func(score vo.Score, wWin string) bool {
		t.Helper()
		updated, _, err := s.uploadBest(ctx, locFull, userA, score, "", 1700000000, wWin, "2026-01", true)
		if err != nil {
			t.Fatalf("uploadBest: %v", err)
		}
		return updated
	}
	up(sc(10, 0, 0), "2026-W01")
	if up(sc(5, 0, 0), "2026-W02") {
		t.Error("all-time best must stay (10,0,0)")
	}
	ms := s.Client().ZRange(ctx, "lb:w:2026-W02:global", 0, -1).Val()
	if len(ms) != 1 {
		t.Fatalf("week W02 global = %v", ms)
	}
	if got, _, _, _ := decodeMember(ms[0]); got != sc(5, 0, 0) {
		t.Errorf("week W02 best = %+v, want (5,0,0)", got)
	}
	// 同一窗口内更差的成绩不覆盖
	up(sc(4, 0, 0), "2026-W02")
	if got, _, _, _ := decodeMember(s.Client().ZRange(ctx, "lb:w:2026-W02:global", 0, -1).Val()[0]); got != sc(5, 0, 0) {
		t.Errorf("same-window worse score overwrote: %+v", got)
	}
	// 月榜同一期:(5,0,0) 不如 (10,0,0)
	if got, _, _, _ := decodeMember(s.Client().ZRange(ctx, "lb:m:2026-01:global", 0, -1).Val()[0]); got != sc(10, 0, 0) {
		t.Errorf("month best = %+v, want (10,0,0)", got)
	}
	// 旧一周不受影响
	if ids := membersOf(t, s, "lb:w:2026-W01:global"); len(ids) != 1 {
		t.Errorf("old week board = %v", ids)
	}
	if win := s.Client().HGet(ctx, "lb:wuser:u_a", "win").Val(); win != "2026-W02" {
		t.Errorf("wuser win = %q, want 2026-W02", win)
	}
}

func TestWindowRegionMoveCleansSameWindow(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()
	locB := &vo.Location{Province: "上海市", City: "上海市", District: "浦东新区"}
	if _, _, err := s.uploadBest(ctx, locFull, userA, sc(1, 0, 0), "", 1700000000, "2026-W01", "2026-01", true); err != nil {
		t.Fatalf("uploadBest: %v", err)
	}
	if _, _, err := s.uploadBest(ctx, locB, userA, sc(2, 0, 0), "", 1700000100, "2026-W01", "2026-01", true); err != nil {
		t.Fatalf("uploadBest: %v", err)
	}
	if ids := membersOf(t, s, "lb:w:2026-W01:prov:浙江省"); len(ids) != 0 {
		t.Errorf("old province week board = %v, want empty", ids)
	}
	if ids := membersOf(t, s, "lb:w:2026-W01:prov:上海市"); len(ids) != 1 {
		t.Errorf("new province week board = %v", ids)
	}
	if n := s.Client().ZCard(ctx, "lb:w:2026-W01:global").Val(); n != 1 {
		t.Errorf("week global card = %d, want 1", n)
	}
}
