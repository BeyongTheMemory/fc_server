package processor

import (
	"context"
	"errors"
	"testing"

	"fc_server/internal/domain/person/application"
	personvo "fc_server/internal/domain/person/entity/vo"
	"fc_server/internal/domain/rank/entity"
	"fc_server/internal/domain/rank/entity/vo"
	"fc_server/internal/processor/dto"
)

func validFetchReq() *dto.FetchRankListRequest {
	return &dto.FetchRankListRequest{
		Location: &dto.LocationDto{Province: "浙江省", City: "杭州市", District: "西湖区"},
	}
}

func score(stage int64) vo.Score {
	return vo.Score{Stage: stage, StageCultivation: vo.CultivationFromInt(stage * 10), TotalCultivation: vo.CultivationFromInt(stage * 100)}
}

func TestFetchRankValidationErrors(t *testing.T) {
	initRank(&fakeStorage{})
	setupPlayers(t)
	req := validFetchReq()
	req.Location.District = "西|湖"
	if _, err := FetchRankList(context.Background(), testUserID, req); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("district with pipe: err = %v, want ErrInvalidParam", err)
	}
	if _, err := FetchRankList(context.Background(), testUserID, nil); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("nil request: err = %v, want ErrInvalidParam", err)
	}
}

func TestFetchRankMapsBoardsAndEnrichesProfiles(t *testing.T) {
	global := entity.NewRankResult("", vo.Nation)
	global.Window = entity.WindowAll
	global.SelfRank = 2
	global.SelfScore = score(5)
	global.ScoreMetaList = []*vo.ScoreMeta{
		{UserInfo: &vo.UserInfo{UserId: "u_top", UserName: "第一名"}, Score: score(9), CreateTime: 111},
		{UserInfo: &vo.UserInfo{UserId: testUserID, UserName: "上次上传时的旧名"}, Score: score(5), CreateTime: 222},
		{UserInfo: &vo.UserInfo{UserId: unnamedUser, UserName: "旧名"}, Score: score(1), CreateTime: 333},
	}
	fake := &fakeStorage{boards: []*entity.RankResult{global}}
	initRank(fake)
	players := setupPlayers(t)
	if _, err := players.UpdateProfile(context.Background(), testUserID,
		personvo.Profile{Nickname: testNickname, AvatarURL: "https://thirdwx.qlogo.cn/a/132"}, 3); err != nil {
		t.Fatalf("seed avatar: %v", err)
	}

	resp, err := FetchRankList(context.Background(), testUserID, validFetchReq())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Hidden || resp.Global == nil || resp.Region == nil || resp.Friend != nil {
		t.Fatalf("boards: global=%v region=%v friend=%v (friend must be null before sync)", resp.Global, resp.Region, resp.Friend)
	}
	if resp.Global.All == nil || resp.Global.Month == nil || resp.Global.Week == nil {
		t.Fatalf("global windows missing: %+v", resp.Global)
	}
	all := resp.Global.All
	if all.Scope != "global" || all.Window != "all" || all.SelfRank != 2 || all.SelfScore == nil || all.SelfScore.Stage != 5 {
		t.Errorf("global all = %+v", all)
	}
	if len(all.ScoreMetaList) != 3 {
		t.Fatalf("entries = %+v", all.ScoreMetaList)
	}
	// 无玩家记录的历史成员:沿用榜单里的昵称,头像为空
	if e := all.ScoreMetaList[0]; e.Rank != 1 || e.UserId != "u_top" || e.UserName != "第一名" || e.AvatarUrl != "" ||
		e.Stage != 9 || e.StageCultivation != "90" || e.TotalCultivation != "900" || e.CreateTime != 111 {
		t.Errorf("legacy entry = %+v", e)
	}
	if e := all.ScoreMetaList[1]; e.Rank != 2 || e.UserName != testNickname || e.AvatarUrl != "https://thirdwx.qlogo.cn/a/132" {
		t.Errorf("profiled entry = %+v", e)
	}
	if e := all.ScoreMetaList[2]; e.UserName != "无名" || e.AvatarUrl != "" {
		t.Errorf("unnamed entry = %+v", e)
	}
	// 未上榜的榜单:self_score 为 null
	if resp.Global.Week.SelfScore != nil || resp.Global.Week.SelfRank != 0 {
		t.Errorf("empty week board = %+v", resp.Global.Week)
	}
	if fake.gotUserId != testUserID {
		t.Errorf("storage received userId = %q", fake.gotUserId)
	}
}

func TestFetchRankFriendBoardAfterSync(t *testing.T) {
	fake := &fakeStorage{memberBests: map[string][]*vo.ScoreMeta{
		entity.WindowAll: {
			{UserInfo: &vo.UserInfo{UserId: testUserID}, Score: score(3), CreateTime: 1},
			{UserInfo: &vo.UserInfo{UserId: unnamedUser}, Score: score(7), CreateTime: 1},
		},
	}}
	initRank(fake)
	_, friends := setupPerson(t)
	friends.Data[testUserID] = []string{unnamedUser}

	resp, err := FetchRankList(context.Background(), testUserID, &dto.FetchRankListRequest{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Region != nil {
		t.Errorf("region must be null without location: %+v", resp.Region)
	}
	if resp.Friend == nil || resp.Friend.All == nil {
		t.Fatalf("friend boards missing: %+v", resp.Friend)
	}
	all := resp.Friend.All
	if all.Scope != "friend" || len(all.ScoreMetaList) != 2 || all.ScoreMetaList[0].UserName != "无名" ||
		all.ScoreMetaList[1].UserName != testNickname || all.SelfRank != 2 {
		t.Errorf("friend all = %+v / %+v", all, all.ScoreMetaList)
	}
}

func TestFetchRankProfileLookupFailure(t *testing.T) {
	global := entity.NewRankResult("", vo.Nation)
	global.ScoreMetaList = []*vo.ScoreMeta{{UserInfo: &vo.UserInfo{UserId: testUserID}, Score: score(1)}}
	initRank(&fakeStorage{boards: []*entity.RankResult{global}})
	players := setupPlayers(t)
	players.Err = errors.New("redis down")
	if _, err := FetchRankList(context.Background(), testUserID, validFetchReq()); !errors.Is(err, application.ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}

func TestMemberIDsDeduplicates(t *testing.T) {
	a := entity.NewRankResult("浙江省", vo.Province)
	a.ScoreMetaList = []*vo.ScoreMeta{{UserInfo: &vo.UserInfo{UserId: "x"}}, {UserInfo: &vo.UserInfo{UserId: "y"}}}
	b := entity.NewRankResult("杭州市", vo.City)
	b.ScoreMetaList = []*vo.ScoreMeta{{UserInfo: &vo.UserInfo{UserId: "y"}}, {UserInfo: &vo.UserInfo{UserId: ""}}}
	ids := memberIDs([]*entity.RankResult{a, b})
	if len(ids) != 2 || ids[0] != "x" || ids[1] != "y" {
		t.Errorf("memberIDs = %v, want [x y]", ids)
	}
}

func TestFetchRankHiddenReturnsNoBoards(t *testing.T) {
	global := entity.NewRankResult("", vo.Nation)
	global.ScoreMetaList = []*vo.ScoreMeta{{UserInfo: &vo.UserInfo{UserId: testUserID}, Score: score(1)}}
	fake := &fakeStorage{boards: []*entity.RankResult{global}}
	initRank(fake)
	setupPlayers(t)
	Init(100_000, true)
	t.Cleanup(func() { Init(100_000, false) })

	resp, err := FetchRankList(context.Background(), testUserID, validFetchReq())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !resp.Hidden || resp.Global != nil || resp.Region != nil || resp.Friend != nil {
		t.Errorf("hidden response = %+v, want hidden with no boards", resp)
	}
	if fake.gotUserId != "" {
		t.Error("hidden leaderboard must not query the boards")
	}
	// 参数仍然校验
	bad := validFetchReq()
	bad.Location.Province = "浙|江"
	if _, err := FetchRankList(context.Background(), testUserID, bad); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("invalid request err = %v", err)
	}
}
