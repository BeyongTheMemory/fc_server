package impl

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"fc_server/internal/domain/person/application"
	"fc_server/internal/domain/person/entity"
	"fc_server/internal/domain/person/entity/vo"
	"fc_server/internal/domain/person/persontest"
	"fc_server/internal/domain/person/provider"
)

const testAppID = "wx_app_test"

type fixture struct {
	app      *UserApplicationImpl
	wechat   *persontest.FakeWechat
	players  *persontest.Players
	sessions *persontest.Sessions
	limiter  *persontest.Limiter
	friends  *persontest.Friends
	clock    time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{
		wechat:   persontest.NewFakeWechat(),
		players:  persontest.NewPlayers(),
		sessions: persontest.NewSessions(),
		limiter:  persontest.NewLimiter(),
		friends:  persontest.NewFriends(),
		clock:    time.Unix(1_800_000_000, 0),
	}
	f.sessions.Now = func() time.Time { return f.clock }
	f.app = New(f.wechat, f.players, f.sessions, f.limiter, f.friends, Options{
		AppID:           testAppID,
		SessionTTL:      7 * 24 * time.Hour,
		LoginRateLimit:  3,
		LoginRateWindow: time.Minute,
		FriendTTL:       30 * 24 * time.Hour,
		MaxFriends:      3,
	})
	f.app.now = func() time.Time { return f.clock }
	f.wechat.OpenIDs["code-a1"] = "openid-a"
	f.wechat.OpenIDs["code-a2"] = "openid-a"
	f.wechat.OpenIDs["code-b"] = "openid-b"
	return f
}

func (f *fixture) login(t *testing.T, code string) *entity.LoginResult {
	t.Helper()
	res, err := f.app.LoginWithWechat(context.Background(), code, "")
	if err != nil {
		t.Fatalf("login(%s): %v", code, err)
	}
	return res
}

func TestLoginCreatesPlayerWithDefaultProfile(t *testing.T) {
	f := newFixture(t)
	res := f.login(t, "code-a1")

	if res.Player.ID != entity.PlayerID(testAppID, "openid-a") {
		t.Errorf("user id = %s, want stable PlayerID", res.Player.ID)
	}
	if res.Player.DisplayName() != entity.DefaultNickname || res.Player.Profile.AvatarURL != "" {
		t.Errorf("new player profile = %+v, want default", res.Player.Profile)
	}
	if len(res.Token) < 43 {
		t.Errorf("token too short: %d chars", len(res.Token))
	}
	if res.ExpiresAt != f.clock.Add(7*24*time.Hour).Unix() {
		t.Errorf("expires_at = %d", res.ExpiresAt)
	}
	if _, ok := f.sessions.Data[res.Token]; ok {
		t.Error("plaintext token must not be used as storage key")
	}
	if _, ok := f.sessions.Data[entity.HashToken(res.Token)]; !ok {
		t.Error("session should be stored under the token hash")
	}
}

func TestRepeatedLoginKeepsIdentityAndProfile(t *testing.T) {
	f := newFixture(t)
	first := f.login(t, "code-a1")
	if _, err := f.app.UpdateProfile(context.Background(), first.Player.ID,
		vo.Profile{Nickname: "阿甘", AvatarURL: "https://thirdwx.qlogo.cn/a.png"}); err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}

	f.clock = f.clock.Add(time.Hour)
	second := f.login(t, "code-a2")
	if second.Player.ID != first.Player.ID {
		t.Fatalf("second login id = %s, want %s", second.Player.ID, first.Player.ID)
	}
	if second.Player.Profile.Nickname != "阿甘" {
		t.Errorf("login must not reset authorized profile, got %+v", second.Player.Profile)
	}
	if second.Token == first.Token {
		t.Error("each login must issue a new token")
	}
	// 多设备并存:旧 token 依然有效
	for _, tok := range []string{first.Token, second.Token} {
		if _, err := f.app.Authenticate(context.Background(), tok); err != nil {
			t.Errorf("Authenticate after second login: %v", err)
		}
	}
}

func TestDifferentOpenIDsGetDifferentPlayers(t *testing.T) {
	f := newFixture(t)
	a := f.login(t, "code-a1")
	b := f.login(t, "code-b")
	if a.Player.ID == b.Player.ID {
		t.Error("different openids must not share a player id")
	}
}

func TestLoginProviderErrorsPassThroughWithoutSession(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{"invalid code", &provider.UpstreamError{Kind: provider.ErrInvalidCode, ErrCode: 40029}, provider.ErrInvalidCode},
		{"risk", &provider.UpstreamError{Kind: provider.ErrRiskBlocked, ErrCode: 40226}, provider.ErrRiskBlocked},
		{"timeout", &provider.UpstreamError{Kind: provider.ErrUpstreamTimeout}, provider.ErrUpstreamTimeout},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.wechat.Err = tt.err
			_, err := f.app.LoginWithWechat(context.Background(), "code-a1", "")
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if len(f.sessions.Data) != 0 || len(f.players.Data) != 0 {
				t.Error("failed exchange must not create player or session")
			}
		})
	}
}

func TestLoginUnknownCodeIsInvalid(t *testing.T) {
	f := newFixture(t)
	if _, err := f.app.LoginWithWechat(context.Background(), "never-issued", ""); !errors.Is(err, provider.ErrInvalidCode) {
		t.Errorf("err = %v, want ErrInvalidCode", err)
	}
}

func TestLoginStorageFailureIssuesNoToken(t *testing.T) {
	f := newFixture(t)
	f.sessions.Err = errors.New("redis down")
	res, err := f.app.LoginWithWechat(context.Background(), "code-a1", "")
	if !errors.Is(err, application.ErrUnavailable) || res != nil {
		t.Fatalf("got (%v, %v), want ErrUnavailable and no result", res, err)
	}

	f = newFixture(t)
	f.players.Err = errors.New("redis down")
	if _, err := f.app.LoginWithWechat(context.Background(), "code-a1", ""); !errors.Is(err, application.ErrUnavailable) {
		t.Errorf("player store failure: err = %v, want ErrUnavailable", err)
	}
}

func TestLoginRateLimitPerClient(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := f.app.LoginWithWechat(ctx, "code-a1", "1.2.3.4"); err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	calls := f.wechat.Calls
	if _, err := f.app.LoginWithWechat(ctx, "code-a1", "1.2.3.4"); !errors.Is(err, application.ErrTooManyAttempts) {
		t.Fatalf("4th attempt err = %v, want ErrTooManyAttempts", err)
	}
	if f.wechat.Calls != calls {
		t.Error("rate-limited attempt must not call wechat")
	}
	if _, err := f.app.LoginWithWechat(ctx, "code-a1", "5.6.7.8"); err != nil {
		t.Errorf("other client should not be limited: %v", err)
	}

	f.limiter.Err = errors.New("redis down")
	if _, err := f.app.LoginWithWechat(ctx, "code-a1", "9.9.9.9"); !errors.Is(err, application.ErrUnavailable) {
		t.Errorf("limiter failure err = %v, want ErrUnavailable", err)
	}
}

func TestAuthenticate(t *testing.T) {
	f := newFixture(t)
	res := f.login(t, "code-a1")
	ctx := context.Background()

	s, err := f.app.Authenticate(ctx, res.Token)
	if err != nil || s.UserID != res.Player.ID {
		t.Fatalf("Authenticate = (%+v, %v)", s, err)
	}
	for name, tok := range map[string]string{
		"empty":    "",
		"unknown":  "not-a-real-token",
		"too long": strings.Repeat("x", 300),
	} {
		if _, err := f.app.Authenticate(ctx, tok); !errors.Is(err, application.ErrUnauthenticated) {
			t.Errorf("%s token: err = %v, want ErrUnauthenticated", name, err)
		}
	}

	f.clock = f.clock.Add(7*24*time.Hour + time.Second)
	if _, err := f.app.Authenticate(ctx, res.Token); !errors.Is(err, application.ErrUnauthenticated) {
		t.Errorf("expired token: err = %v, want ErrUnauthenticated", err)
	}
}

func TestAuthenticateRejectsOtherAppSession(t *testing.T) {
	f := newFixture(t)
	res := f.login(t, "code-a1")
	other := New(f.wechat, f.players, f.sessions, f.limiter, f.friends, Options{AppID: "wx_other", SessionTTL: time.Hour})
	other.now = func() time.Time { return f.clock }
	if _, err := other.Authenticate(context.Background(), res.Token); !errors.Is(err, application.ErrUnauthenticated) {
		t.Errorf("cross-app token: err = %v, want ErrUnauthenticated", err)
	}
}

func TestAuthenticateStorageFailureIsUnavailable(t *testing.T) {
	f := newFixture(t)
	res := f.login(t, "code-a1")
	f.sessions.Err = errors.New("redis down")
	if _, err := f.app.Authenticate(context.Background(), res.Token); !errors.Is(err, application.ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}

func TestUpdateProfile(t *testing.T) {
	f := newFixture(t)
	res := f.login(t, "code-a1")
	ctx := context.Background()

	p, err := f.app.UpdateProfile(ctx, res.Player.ID, vo.Profile{Nickname: "阿甘🎴", AvatarURL: ""})
	if err != nil || p.DisplayName() != "阿甘🎴" {
		t.Fatalf("UpdateProfile = (%+v, %v)", p, err)
	}
	rev := p.Revision
	p, err = f.app.UpdateProfile(ctx, res.Player.ID, vo.Profile{Nickname: "阿甘🎴"})
	if err != nil || p.Revision != rev {
		t.Errorf("identical update bumped revision %d → %d (err %v)", rev, p.Revision, err)
	}

	if _, err := f.app.UpdateProfile(ctx, res.Player.ID, vo.Profile{Nickname: ""}); !errors.Is(err, application.ErrInvalidProfile) {
		t.Errorf("empty nickname err = %v, want ErrInvalidProfile", err)
	}
	if _, err := f.app.UpdateProfile(ctx, "wx_missing", vo.Profile{Nickname: "x"}); !errors.Is(err, application.ErrUnauthenticated) {
		t.Errorf("missing player err = %v, want ErrUnauthenticated", err)
	}
}

func TestLogoutOnlyRemovesCurrentSession(t *testing.T) {
	f := newFixture(t)
	a1 := f.login(t, "code-a1")
	a2 := f.login(t, "code-a2")
	ctx := context.Background()

	if err := f.app.Logout(ctx, a1.Token); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := f.app.Authenticate(ctx, a1.Token); !errors.Is(err, application.ErrUnauthenticated) {
		t.Errorf("logged-out token err = %v, want ErrUnauthenticated", err)
	}
	if _, err := f.app.Authenticate(ctx, a2.Token); err != nil {
		t.Errorf("other device token should survive logout: %v", err)
	}
	if _, err := f.app.GetPlayer(ctx, a1.Player.ID); err != nil {
		t.Errorf("logout must not delete player: %v", err)
	}
}

func TestGetPlayers(t *testing.T) {
	f := newFixture(t)
	a := f.login(t, "code-a1")
	got, err := f.app.GetPlayers(context.Background(), []string{a.Player.ID, "wx_missing"})
	if err != nil || len(got) != 1 || got[a.Player.ID] == nil {
		t.Fatalf("GetPlayers = (%v, %v)", got, err)
	}
	f.players.Err = errors.New("redis down")
	if _, err := f.app.GetPlayers(context.Background(), []string{a.Player.ID}); !errors.Is(err, application.ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}

func TestSyncWechatFriends(t *testing.T) {
	f := newFixture(t)
	me := f.login(t, "code-a1")
	ctx := context.Background()

	if ids, err := f.app.GetFriendIDs(ctx, me.Player.ID); err != nil || ids != nil {
		t.Fatalf("before sync = (%v, %v), want nil", ids, err)
	}

	f.wechat.Friends["openid-a"] = []string{"openid-b", "openid-c", "openid-b", "openid-a", ""}
	n, err := f.app.SyncWechatFriends(ctx, me.Player.ID, "code-a2", "enc", "iv")
	if err != nil || n != 2 {
		t.Fatalf("SyncWechatFriends = (%d, %v), want 2 (deduplicated, self excluded)", n, err)
	}
	ids, err := f.app.GetFriendIDs(ctx, me.Player.ID)
	if err != nil || len(ids) != 2 || ids[0] != entity.PlayerID(testAppID, "openid-b") || ids[1] != entity.PlayerID(testAppID, "openid-c") {
		t.Errorf("friend ids = %v (err %v), want player ids of b and c", ids, err)
	}

	// 好友上限
	f.wechat.Friends["openid-a"] = []string{"o1", "o2", "o3", "o4", "o5"}
	if n, _ := f.app.SyncWechatFriends(ctx, me.Player.ID, "code-a2", "enc", "iv"); n != 3 {
		t.Errorf("capped friend count = %d, want 3", n)
	}

	// 同步结果为空也算已同步
	f.wechat.Friends["openid-a"] = nil
	if _, err := f.app.SyncWechatFriends(ctx, me.Player.ID, "code-a2", "enc", "iv"); err != nil {
		t.Fatalf("empty sync: %v", err)
	}
	if ids, _ := f.app.GetFriendIDs(ctx, me.Player.ID); ids == nil || len(ids) != 0 {
		t.Errorf("after empty sync = %v, want empty non-nil", ids)
	}
}

func TestSyncWechatFriendsRejectsOtherPlayersData(t *testing.T) {
	f := newFixture(t)
	me := f.login(t, "code-a1")
	// code-b 属于另一个微信号
	if _, err := f.app.SyncWechatFriends(context.Background(), me.Player.ID, "code-b", "enc", "iv"); !errors.Is(err, application.ErrInvalidFriendData) {
		t.Errorf("err = %v, want ErrInvalidFriendData", err)
	}
	if _, synced := f.friends.Data[me.Player.ID]; synced {
		t.Error("friends must not be saved for mismatched data")
	}
}

func TestSyncWechatFriendsErrors(t *testing.T) {
	f := newFixture(t)
	me := f.login(t, "code-a1")
	ctx := context.Background()

	f.wechat.FriendErr = &provider.UpstreamError{Kind: provider.ErrInvalidEncryptedData}
	if _, err := f.app.SyncWechatFriends(ctx, me.Player.ID, "code-a2", "enc", "iv"); !errors.Is(err, provider.ErrInvalidEncryptedData) {
		t.Errorf("decrypt failure err = %v", err)
	}
	f.wechat.FriendErr = nil
	f.friends.Err = errors.New("redis down")
	if _, err := f.app.SyncWechatFriends(ctx, me.Player.ID, "code-a2", "enc", "iv"); !errors.Is(err, application.ErrUnavailable) {
		t.Errorf("storage failure err = %v, want ErrUnavailable", err)
	}
	if _, err := f.app.GetFriendIDs(ctx, me.Player.ID); !errors.Is(err, application.ErrUnavailable) {
		t.Errorf("list failure err = %v, want ErrUnavailable", err)
	}
	if _, err := f.app.SyncWechatFriends(ctx, "wx_ghost", "code-a2", "enc", "iv"); !errors.Is(err, application.ErrUnauthenticated) {
		t.Errorf("unknown player err = %v, want ErrUnauthenticated", err)
	}
}
