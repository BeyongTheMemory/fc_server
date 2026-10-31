package processor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"fc_server/internal/domain/person/application"
	personimpl "fc_server/internal/domain/person/application/impl"
	personvo "fc_server/internal/domain/person/entity/vo"
	"fc_server/internal/domain/person/persontest"
	"fc_server/internal/domain/rank/application/impl"
	"fc_server/internal/domain/rank/entity"
	"fc_server/internal/domain/rank/entity/vo"
	"fc_server/internal/domain/rank/repository"
	runapp "fc_server/internal/domain/run/application"
	runimpl "fc_server/internal/domain/run/application/impl"
	runentity "fc_server/internal/domain/run/entity"
	"fc_server/internal/domain/run/runtest"
	"fc_server/internal/processor/dto"
)

type fakeStorage struct {
	uploadUpdated bool
	uploadBest    vo.Score
	uploadErr     error
	boards        []*entity.RankResult // 按 GetBoards 的 refs 顺序依次返回
	memberBests   map[string][]*vo.ScoreMeta
	boardsErr     error
	gotLocation   *vo.Location
	gotUser       *vo.UserInfo
	gotScore      vo.Score
	gotUserId     string
	gotIP         string
}

func (f *fakeStorage) UploadBest(_ context.Context, loc *vo.Location, user *vo.UserInfo, score vo.Score, clientIP string) (bool, vo.Score, error) {
	f.gotLocation, f.gotUser, f.gotScore, f.gotIP = loc, user, score, clientIP
	return f.uploadUpdated, f.uploadBest, f.uploadErr
}

func (f *fakeStorage) CountMembers(_ context.Context, refs []repository.BoardRef) ([]int64, error) {
	return make([]int64, len(refs)), f.boardsErr
}

func (f *fakeStorage) GetBoards(_ context.Context, refs []repository.BoardRef, userId string, _ int) ([]*entity.RankResult, error) {
	f.gotUserId = userId
	if f.boardsErr != nil {
		return nil, f.boardsErr
	}
	out := make([]*entity.RankResult, len(refs))
	for i, r := range refs {
		if i < len(f.boards) {
			out[i] = f.boards[i]
			continue
		}
		b := entity.NewRankResult("", r.Level)
		b.Window = r.Window
		out[i] = b
	}
	return out, nil
}

func (f *fakeStorage) GetMemberBests(_ context.Context, window string, _ []string) ([]*vo.ScoreMeta, error) {
	return f.memberBests[window], f.boardsErr
}

const (
	testUserID   = "wx_u1"
	unnamedUser  = "wx_unnamed"
	testNickname = "阿甘"
)

// setupPlayers 初始化 person 应用:testUserID 已授权昵称,unnamedUser 未授权资料。
func setupPlayers(t *testing.T) *persontest.Players {
	t.Helper()
	players, _ := setupPerson(t)
	return players
}

func setupPerson(t *testing.T) (*persontest.Players, *persontest.Friends) {
	t.Helper()
	ctx := context.Background()
	players := persontest.NewPlayers()
	friends := persontest.NewFriends()
	personimpl.Init(persontest.NewFakeWechat(), players, persontest.NewSessions(), persontest.NewLimiter(), friends,
		personimpl.Options{AppID: "wx_app", SessionTTL: time.Hour, LoginRateLimit: 10, LoginRateWindow: time.Minute,
			FriendTTL: time.Hour, MaxFriends: 100})
	for _, id := range []string{testUserID, unnamedUser} {
		if _, err := players.UpsertOnLogin(ctx, id, "wx_app", "openid-"+id, 1); err != nil {
			t.Fatalf("seed player: %v", err)
		}
	}
	if _, err := players.UpdateProfile(ctx, testUserID, personvo.Profile{Nickname: testNickname}, 2); err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	return players, friends
}

var (
	testRuns *runtest.Runs
	runSeq   int
)

func initRank(fake *fakeStorage) {
	impl.Init(fake, 100, 10)
	Init(100_000, false)
	testRuns = runtest.NewRuns()
	runimpl.Init(testRuns, runtest.NewLimiter(), runimpl.Options{TicketTTL: time.Hour, StartLimit: 1000, StartWindow: time.Hour})
}

// newRun 为 user 签发一张对局票。
func newRun(user string) string {
	runSeq++
	id := fmt.Sprintf("%032x", runSeq)
	testRuns.Data[id] = &runentity.Run{ID: id, UserID: user}
	return id
}

func validUploadReq() *dto.UploadScoreRequest {
	return validUploadReqFor(testUserID)
}

func validUploadReqFor(user string) *dto.UploadScoreRequest {
	return &dto.UploadScoreRequest{
		RunId:            newRun(user),
		Location:         &dto.LocationDto{Province: "浙江省", City: "杭州市", District: "西湖区"},
		Stage:            12,
		StageCultivation: json.RawMessage(`3400`),
		TotalCultivation: json.RawMessage(`"98000"`),
	}
}

func TestUploadScoreValidationErrors(t *testing.T) {
	initRank(&fakeStorage{})
	setupPlayers(t)

	tests := []struct {
		name   string
		mutate func(r *dto.UploadScoreRequest)
	}{
		{"zero stage", func(r *dto.UploadScoreRequest) { r.Stage = 0 }},
		{"stage above max", func(r *dto.UploadScoreRequest) { r.Stage = 100_001 }},
		{"missing stage cultivation", func(r *dto.UploadScoreRequest) { r.StageCultivation = nil }},
		{"null total", func(r *dto.UploadScoreRequest) { r.TotalCultivation = json.RawMessage(`null`) }},
		{"negative stage cultivation", func(r *dto.UploadScoreRequest) { r.StageCultivation = json.RawMessage(`-1`) }},
		{"negative string", func(r *dto.UploadScoreRequest) { r.TotalCultivation = json.RawMessage(`"-5"`) }},
		{"fraction number", func(r *dto.UploadScoreRequest) { r.StageCultivation = json.RawMessage(`1.5`) }},
		{"fraction string", func(r *dto.UploadScoreRequest) { r.StageCultivation = json.RawMessage(`"1.5"`) }},
		{"number exponent", func(r *dto.UploadScoreRequest) { r.TotalCultivation = json.RawMessage(`1e21`) }},
		{"unsafe json number", func(r *dto.UploadScoreRequest) { r.TotalCultivation = json.RawMessage(`9007199254740992`) }},
		{"bool", func(r *dto.UploadScoreRequest) { r.TotalCultivation = json.RawMessage(`true`) }},
		{"too many digits", func(r *dto.UploadScoreRequest) {
			r.TotalCultivation = json.RawMessage(`"` + strings.Repeat("9", 321) + `"`)
		}},
		{"total below last", func(r *dto.UploadScoreRequest) { r.TotalCultivation = json.RawMessage(`"3399"`) }},
		{"province with pipe", func(r *dto.UploadScoreRequest) { r.Location.Province = "浙|江" }},
		{"region too long", func(r *dto.UploadScoreRequest) { r.Location.City = strings.Repeat("市", 33) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validUploadReq()
			tt.mutate(req)
			if _, err := UploadScore(context.Background(), testUserID, "203.0.113.9", req); !errors.Is(err, ErrInvalidParam) {
				t.Errorf("err = %v, want ErrInvalidParam", err)
			}
		})
	}
	if _, err := UploadScore(context.Background(), testUserID, "203.0.113.9", nil); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("nil request: err = %v, want ErrInvalidParam", err)
	}
	// 修为为 0 合法
	req := validUploadReq()
	req.StageCultivation, req.TotalCultivation = json.RawMessage(`0`), json.RawMessage(`"0"`)
	if _, err := UploadScore(context.Background(), testUserID, "203.0.113.9", req); err != nil {
		t.Errorf("zero cultivation should be valid: %v", err)
	}
}

func TestUploadScoreBigCultivationFormats(t *testing.T) {
	tests := []struct {
		name        string
		last, total string
		wantLast    vo.Cultivation
		wantTotal   vo.Cultivation
	}{
		{"safe json integer", `9007199254740991`, `9007199254740991`, "9007199254740991", "9007199254740991"},
		{"decimal string beyond int64", `"931009731811524"`, `"123456789012345678901234567890"`,
			"931009731811524", "123456789012345678901234567890"},
		{"js scientific notation", `"9.31009731811524e+23"`, `"1.2345678901234567e+25"`,
			vo.Cultivation("931009731811524" + strings.Repeat("0", 9)), vo.Cultivation("12345678901234567" + strings.Repeat("0", 9))},
		{"leading zeros normalized", `"000123"`, `"0123"`, "123", "123"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeStorage{uploadUpdated: true}
			initRank(fake)
			setupPlayers(t)
			req := validUploadReq()
			req.StageCultivation, req.TotalCultivation = json.RawMessage(tt.last), json.RawMessage(tt.total)
			if _, err := UploadScore(context.Background(), testUserID, "203.0.113.9", req); err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if fake.gotScore.StageCultivation != tt.wantLast || fake.gotScore.TotalCultivation != tt.wantTotal {
				t.Errorf("parsed = %+v", fake.gotScore)
			}
		})
	}
}

func TestUploadScoreUsesAuthenticatedIdentityAndServerNickname(t *testing.T) {
	best := vo.Score{Stage: 20, StageCultivation: "1", TotalCultivation: vo.Cultivation("2" + strings.Repeat("0", 30))}
	fake := &fakeStorage{uploadUpdated: false, uploadBest: best}
	initRank(fake)
	setupPlayers(t)

	resp, err := UploadScore(context.Background(), testUserID, "203.0.113.9", validUploadReq())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Updated || resp.Best == nil || resp.Best.Stage != 20 || resp.Best.StageCultivation != "1" ||
		resp.Best.TotalCultivation != "2"+strings.Repeat("0", 30) {
		t.Errorf("resp = %+v / best %+v", resp, resp.Best)
	}
	if fake.gotScore != (vo.Score{Stage: 12, StageCultivation: "3400", TotalCultivation: "98000"}) ||
		fake.gotUser.UserId != testUserID || fake.gotUser.UserName != testNickname || fake.gotLocation.Province != "浙江省" ||
		fake.gotIP != "203.0.113.9" {
		t.Errorf("storage received: score=%+v user=%+v loc=%+v", fake.gotScore, fake.gotUser, fake.gotLocation)
	}
}

func TestUploadScoreUnnamedPlayerUsesDefaultNickname(t *testing.T) {
	fake := &fakeStorage{uploadUpdated: true}
	initRank(fake)
	setupPlayers(t)
	if _, err := UploadScore(context.Background(), unnamedUser, "203.0.113.9", validUploadReqFor(unnamedUser)); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if fake.gotUser.UserName != "无名" {
		t.Errorf("user name = %q, want 无名", fake.gotUser.UserName)
	}
}

func TestUploadScoreWithoutLocationIsAllowed(t *testing.T) {
	fake := &fakeStorage{uploadUpdated: true}
	initRank(fake)
	setupPlayers(t)
	req := validUploadReq()
	req.Location = nil
	if _, err := UploadScore(context.Background(), testUserID, "203.0.113.9", req); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if fake.gotLocation.Province != "" {
		t.Errorf("location = %+v, want empty", fake.gotLocation)
	}
}

func TestUploadScoreUnknownPlayerIsUnauthenticated(t *testing.T) {
	fake := &fakeStorage{}
	initRank(fake)
	setupPlayers(t)
	if _, err := UploadScore(context.Background(), "wx_ghost", "203.0.113.9", validUploadReqFor("wx_ghost")); !errors.Is(err, application.ErrUnauthenticated) {
		t.Fatalf("err = %v, want ErrUnauthenticated", err)
	}
	if fake.gotUser != nil {
		t.Error("score must not be written for an unknown player")
	}
}

func TestUploadScoreInternalErrorIsNotInvalidParam(t *testing.T) {
	initRank(&fakeStorage{uploadErr: errors.New("redis down")})
	setupPlayers(t)
	_, err := UploadScore(context.Background(), testUserID, "203.0.113.9", validUploadReq())
	if err == nil || errors.Is(err, ErrInvalidParam) {
		t.Errorf("err = %v, want internal error (not ErrInvalidParam)", err)
	}
}

func TestUploadScoreWithProfileSavesBeforeRanking(t *testing.T) {
	fake := &fakeStorage{uploadUpdated: true}
	initRank(fake)
	players := setupPlayers(t)

	req := validUploadReqFor(unnamedUser)
	req.Profile = &dto.ProfileDto{Nickname: "新昵称🎴", AvatarUrl: "https://thirdwx.qlogo.cn/n/132"}
	if _, err := UploadScore(context.Background(), unnamedUser, "203.0.113.9", req); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if fake.gotUser.UserName != "新昵称🎴" {
		t.Errorf("rank name = %q, want the submitted profile nickname", fake.gotUser.UserName)
	}
	saved, _ := players.Get(context.Background(), unnamedUser)
	if saved.Profile.Nickname != "新昵称🎴" || saved.Profile.AvatarURL != "https://thirdwx.qlogo.cn/n/132" {
		t.Errorf("profile not saved: %+v", saved.Profile)
	}
	updates := players.Updates
	if _, err := UploadScore(context.Background(), unnamedUser, "203.0.113.9", req); err != nil {
		t.Fatalf("repeat upload: %v", err)
	}
	if players.Updates != updates {
		t.Error("identical profile must not create a new revision")
	}
}

func TestUploadScoreInvalidProfileRejectsWholeRequest(t *testing.T) {
	fake := &fakeStorage{}
	initRank(fake)
	players := setupPlayers(t)

	req := validUploadReq()
	req.Profile = &dto.ProfileDto{Nickname: "x", AvatarUrl: "http://insecure.example/a.png"}
	if _, err := UploadScore(context.Background(), testUserID, "203.0.113.9", req); !errors.Is(err, application.ErrInvalidProfile) {
		t.Fatalf("err = %v, want ErrInvalidProfile", err)
	}
	if fake.gotUser != nil {
		t.Error("score must not be written when profile is invalid")
	}
	req = validUploadReq()
	req.Stage = 0
	req.Profile = &dto.ProfileDto{Nickname: "不该保存"}
	if _, err := UploadScore(context.Background(), testUserID, "203.0.113.9", req); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("err = %v, want ErrInvalidParam", err)
	}
	if p, _ := players.Get(context.Background(), testUserID); p.Profile.Nickname != testNickname {
		t.Errorf("profile saved despite invalid score: %+v", p.Profile)
	}
}

func TestUploadScoreRunTicketRules(t *testing.T) {
	fake := &fakeStorage{uploadUpdated: true, uploadBest: vo.Score{Stage: 12, StageCultivation: "3400", TotalCultivation: "98000"}}
	initRank(fake)
	setupPlayers(t)
	ctx := context.Background()

	// 首次成功;相同成绩重试仍成功;换成绩 409
	req := validUploadReq()
	if resp, err := UploadScore(ctx, testUserID, "203.0.113.9", req); err != nil || !resp.Updated {
		t.Fatalf("upload = (%+v, %v)", resp, err)
	}
	if _, err := UploadScore(ctx, testUserID, "203.0.113.9", req); err != nil {
		t.Errorf("retry with the same score should succeed: %v", err)
	}
	changed := *req
	changed.Stage = 13
	if _, err := UploadScore(ctx, testUserID, "203.0.113.9", &changed); !errors.Is(err, runapp.ErrRunConflict) {
		t.Errorf("different score for the same run err = %v, want ErrRunConflict", err)
	}

	// 别人的票、伪造的票、缺少 run_id
	others := validUploadReqFor(unnamedUser)
	if _, err := UploadScore(ctx, testUserID, "203.0.113.9", others); !errors.Is(err, runapp.ErrRunNotFound) {
		t.Errorf("another player's run err = %v", err)
	}
	forged := validUploadReq()
	forged.RunId = fmt.Sprintf("%032x", 999999)
	if _, err := UploadScore(ctx, testUserID, "203.0.113.9", forged); !errors.Is(err, runapp.ErrRunNotFound) {
		t.Errorf("unknown run err = %v", err)
	}
	missing := validUploadReq()
	missing.RunId = ""
	if _, err := UploadScore(ctx, testUserID, "203.0.113.9", missing); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("missing run_id err = %v", err)
	}
}
