package impl

import (
	"context"
	"errors"
	"testing"

	"fc_server/internal/domain/rank/entity"
	"fc_server/internal/domain/rank/entity/vo"
	"fc_server/internal/domain/rank/repository"
)

type fakeStorage struct {
	best       map[string]vo.Score
	cityCounts map[string]int64 // window → 城市榜人数
	boards     map[string][]*vo.ScoreMeta
	members    map[string][]*vo.ScoreMeta // window → 成员当期成绩
	gotRefs    []repository.BoardRef
	gotLimit   int
	gotUserId  string
	gotMembers []string
	err        error
}

func newFakeStorage() *fakeStorage {
	return &fakeStorage{
		best:       map[string]vo.Score{},
		cityCounts: map[string]int64{},
		boards:     map[string][]*vo.ScoreMeta{},
		members:    map[string][]*vo.ScoreMeta{},
	}
}

func (f *fakeStorage) UploadBest(_ context.Context, _ *vo.Location, user *vo.UserInfo, score vo.Score, _ string) (bool, vo.Score, error) {
	if f.err != nil {
		return false, vo.Score{}, f.err
	}
	cur, ok := f.best[user.UserId]
	if ok && cur.Compare(score) >= 0 {
		return false, cur, nil
	}
	f.best[user.UserId] = score
	return true, score, nil
}

func (f *fakeStorage) CountMembers(_ context.Context, refs []repository.BoardRef) ([]int64, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]int64, len(refs))
	for i, r := range refs {
		if r.Level == vo.City {
			out[i] = f.cityCounts[r.Window]
		}
	}
	return out, nil
}

func (f *fakeStorage) GetBoards(_ context.Context, refs []repository.BoardRef, userId string, limit int) ([]*entity.RankResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.gotRefs, f.gotLimit, f.gotUserId = refs, limit, userId
	out := make([]*entity.RankResult, len(refs))
	for i, r := range refs {
		b := entity.NewRankResult(r.Level.GetLocationValue(locOrEmpty(r.Location)), r.Level)
		b.Window = r.Window
		b.ScoreMetaList = append(b.ScoreMetaList, f.boards[r.Window]...)
		out[i] = b
	}
	return out, nil
}

func (f *fakeStorage) GetMemberBests(_ context.Context, window string, userIds []string) ([]*vo.ScoreMeta, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.gotMembers = userIds
	want := map[string]bool{}
	for _, id := range userIds {
		want[id] = true
	}
	var out []*vo.ScoreMeta
	for _, m := range f.members[window] {
		if want[m.UserInfo.UserId] {
			out = append(out, m)
		}
	}
	return out, nil
}

func locOrEmpty(l *vo.Location) *vo.Location {
	if l == nil {
		return &vo.Location{}
	}
	return l
}

func meta(id string, score vo.Score, at int64) *vo.ScoreMeta {
	return &vo.ScoreMeta{UserInfo: &vo.UserInfo{UserId: id, UserName: "n_" + id}, Score: score, CreateTime: at}
}

func sc(stage, last, total int64) vo.Score {
	return vo.Score{Stage: stage, StageCultivation: vo.CultivationFromInt(last), TotalCultivation: vo.CultivationFromInt(total)}
}

var hangzhou = &vo.Location{Province: "浙江省", City: "杭州市", District: "西湖区"}

func TestUploadScoreOnlyKeepsBest(t *testing.T) {
	fake := newFakeStorage()
	Init(fake, 100, 10)
	app := GetRankApplicationImpl()
	user := &vo.UserInfo{UserId: "u_1", UserName: "阿甘"}

	tests := []struct {
		name        string
		score       vo.Score
		wantUpdated bool
		wantBest    vo.Score
	}{
		{"first upload updates", sc(3, 10, 100), true, sc(3, 10, 100)},
		{"worse keeps best", sc(3, 10, 99), false, sc(3, 10, 100)},
		{"equal keeps best (idempotent)", sc(3, 10, 100), false, sc(3, 10, 100)},
		{"better updates", sc(4, 0, 0), true, sc(4, 0, 0)},
	}
	for _, tt := range tests {
		updated, best, err := app.UploadScore(context.Background(), hangzhou, user, tt.score, "203.0.113.9")
		if err != nil || updated != tt.wantUpdated || best != tt.wantBest {
			t.Errorf("%s: got (%v,%+v,%v), want (%v,%+v)", tt.name, updated, best, err, tt.wantUpdated, tt.wantBest)
		}
	}
}

func TestGetRankRegionUsesCityWhenEnoughPlayers(t *testing.T) {
	fake := newFakeStorage()
	fake.cityCounts = map[string]int64{entity.WindowAll: 10, entity.WindowMonth: 9, entity.WindowWeek: 0}
	Init(fake, 100, 10)

	lb, err := GetRankApplicationImpl().GetRank(context.Background(), hangzhou, "u_1", nil)
	if err != nil {
		t.Fatalf("GetRank: %v", err)
	}
	if len(lb.Global) != 3 || len(lb.Region) != 3 || lb.Friend != nil {
		t.Fatalf("boards = %d/%d/%v", len(lb.Global), len(lb.Region), lb.Friend)
	}
	for i, w := range entity.Windows {
		if lb.Global[i].Scope != entity.ScopeGlobal || lb.Global[i].Window != w {
			t.Errorf("global[%d] = %s/%s", i, lb.Global[i].Scope, lb.Global[i].Window)
		}
	}
	// 每个窗口独立判断:总榜城市够 10 人用城市;月榜 9 人、周榜 0 人退回省份
	want := []struct{ scope, address string }{
		{entity.ScopeCity, "杭州市"}, {entity.ScopeProvince, "浙江省"}, {entity.ScopeProvince, "浙江省"},
	}
	for i, w := range want {
		if lb.Region[i].Scope != w.scope || lb.Region[i].Address != w.address || lb.Region[i].Window != entity.Windows[i] {
			t.Errorf("region[%d] = %s/%s/%s, want %s/%s", i, lb.Region[i].Scope, lb.Region[i].Address, lb.Region[i].Window, w.scope, w.address)
		}
	}
}

func TestGetRankRegionWithPartialLocation(t *testing.T) {
	fake := newFakeStorage()
	Init(fake, 100, 10)
	app := GetRankApplicationImpl()

	lb, err := app.GetRank(context.Background(), &vo.Location{Province: "浙江省"}, "u", nil)
	if err != nil || len(lb.Region) != 3 || lb.Region[0].Scope != entity.ScopeProvince {
		t.Fatalf("province-only = (%+v, %v)", lb, err)
	}
	for _, loc := range []*vo.Location{nil, {}, {City: "杭州市"}} {
		lb, err := app.GetRank(context.Background(), loc, "u", nil)
		if err != nil || lb.Region != nil || len(lb.Global) != 3 {
			t.Errorf("location %+v: region = %v (err %v), want nil with global boards", loc, lb.Region, err)
		}
	}
}

func TestGetRankZeroThresholdAlwaysCity(t *testing.T) {
	fake := newFakeStorage()
	Init(fake, 100, 0)
	lb, _ := GetRankApplicationImpl().GetRank(context.Background(), hangzhou, "u", nil)
	for _, b := range lb.Region {
		if b.Scope != entity.ScopeCity {
			t.Errorf("threshold 0 should always use city, got %s", b.Scope)
		}
	}
}

func TestGetRankSortsByScoreThenTime(t *testing.T) {
	fake := newFakeStorage()
	fake.boards[entity.WindowAll] = []*vo.ScoreMeta{
		meta("u_low", sc(1, 0, 0), 1),
		meta("u_late", sc(5, 5, 5), 200),
		meta("u_top", sc(6, 0, 0), 300),
		meta("u_early", sc(5, 5, 5), 100),
		meta("u_mid", sc(5, 6, 0), 50),
	}
	Init(fake, 100, 10)
	lb, err := GetRankApplicationImpl().GetRank(context.Background(), hangzhou, "u_top", nil)
	if err != nil {
		t.Fatalf("GetRank: %v", err)
	}
	got := lb.Global[0].ScoreMetaList
	for i, want := range []string{"u_top", "u_mid", "u_early", "u_late", "u_low"} {
		if got[i].UserInfo.UserId != want {
			t.Errorf("pos %d = %s, want %s", i, got[i].UserInfo.UserId, want)
		}
	}
}

func TestGetRankPassesLimitAndUserId(t *testing.T) {
	fake := newFakeStorage()
	Init(fake, 42, 10)
	if _, err := GetRankApplicationImpl().GetRank(context.Background(), hangzhou, "u_9", nil); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if fake.gotLimit != 42 || fake.gotUserId != "u_9" || len(fake.gotRefs) != 6 {
		t.Errorf("limit/user/refs = %d/%s/%d", fake.gotLimit, fake.gotUserId, len(fake.gotRefs))
	}
}

func TestGetRankFriendBoards(t *testing.T) {
	fake := newFakeStorage()
	fake.members[entity.WindowAll] = []*vo.ScoreMeta{
		meta("f_1", sc(9, 0, 0), 10),
		meta("me", sc(3, 0, 0), 10),
		meta("f_2", sc(5, 0, 0), 10),
		meta("stranger", sc(99, 0, 0), 10),
	}
	fake.members[entity.WindowWeek] = []*vo.ScoreMeta{meta("f_2", sc(1, 0, 0), 10)}
	Init(fake, 2, 10)

	lb, err := GetRankApplicationImpl().GetRank(context.Background(), hangzhou, "me", []string{"f_1", "f_2", "f_1", "me"})
	if err != nil {
		t.Fatalf("GetRank: %v", err)
	}
	if len(lb.Friend) != 3 {
		t.Fatalf("friend boards = %d, want 3", len(lb.Friend))
	}
	if len(fake.gotMembers) != 3 {
		t.Errorf("members queried = %v, want deduplicated me+f_1+f_2", fake.gotMembers)
	}
	all := lb.Friend[0]
	if all.Scope != entity.ScopeFriend || all.Window != entity.WindowAll {
		t.Errorf("friend board = %s/%s", all.Scope, all.Window)
	}
	// limit 2 截断,但自己第 3 名仍可见;非好友不出现
	if len(all.ScoreMetaList) != 2 || all.ScoreMetaList[0].UserInfo.UserId != "f_1" || all.ScoreMetaList[1].UserInfo.UserId != "f_2" {
		t.Errorf("friend all entries = %+v", all.ScoreMetaList)
	}
	if all.SelfRank != 3 || all.SelfScore != sc(3, 0, 0) {
		t.Errorf("self = %d %+v, want 3 (3,0,0)", all.SelfRank, all.SelfScore)
	}
	// 本周自己没有成绩
	if week := lb.Friend[2]; week.SelfRank != 0 || len(week.ScoreMetaList) != 1 {
		t.Errorf("friend week = %+v", week)
	}
}

func TestGetRankNoFriendsStillHasSelf(t *testing.T) {
	fake := newFakeStorage()
	fake.members[entity.WindowAll] = []*vo.ScoreMeta{meta("me", sc(1, 0, 0), 1)}
	Init(fake, 100, 10)
	lb, err := GetRankApplicationImpl().GetRank(context.Background(), hangzhou, "me", []string{})
	if err != nil || lb.Friend == nil || lb.Friend[0].SelfRank != 1 {
		t.Fatalf("empty friend list = (%+v, %v), want board with self", lb.Friend, err)
	}
}

func TestErrorsPropagate(t *testing.T) {
	fake := newFakeStorage()
	fake.err = errors.New("redis down")
	Init(fake, 100, 10)
	app := GetRankApplicationImpl()
	if _, _, err := app.UploadScore(context.Background(), hangzhou, &vo.UserInfo{UserId: "u"}, sc(1, 0, 0), ""); err == nil {
		t.Error("UploadScore should propagate storage error")
	}
	if _, err := app.GetRank(context.Background(), hangzhou, "u", nil); err == nil {
		t.Error("GetRank should propagate storage error")
	}
}
