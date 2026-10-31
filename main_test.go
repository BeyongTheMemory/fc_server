package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"fc_server/internal/config"
	achievementimpl "fc_server/internal/domain/achievement/application/impl"
	achievemententity "fc_server/internal/domain/achievement/entity"
	personimpl "fc_server/internal/domain/person/application/impl"
	"fc_server/internal/domain/person/entity"
	"fc_server/internal/domain/person/persontest"
	"fc_server/internal/domain/person/provider"
	"fc_server/internal/domain/rank/application/impl"
	rankentity "fc_server/internal/domain/rank/entity"
	"fc_server/internal/domain/rank/entity/vo"
	rankrepo "fc_server/internal/domain/rank/repository"
	recordimpl "fc_server/internal/domain/record/application/impl"
	recordentity "fc_server/internal/domain/record/entity"
	recordrepo "fc_server/internal/domain/record/repository"
	runimpl "fc_server/internal/domain/run/application/impl"
	"fc_server/internal/domain/run/runtest"
	"fc_server/internal/infrastructure/repository_impl"
	"fc_server/internal/processor"
	"fc_server/internal/util"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

const testAppID = "wx_app_test"

type fakeRankStorage struct {
	best      map[string]vo.Score
	names     map[string]string
	ips       map[string]string
	boards    []*rankentity.RankResult // 依次作为 GetBoards 前几个榜单的结果
	gotUserId string
}

func (f *fakeRankStorage) UploadBest(_ context.Context, _ *vo.Location, user *vo.UserInfo, score vo.Score, clientIP string) (bool, vo.Score, error) {
	if f.best == nil {
		f.best, f.names, f.ips = map[string]vo.Score{}, map[string]string{}, map[string]string{}
	}
	f.names[user.UserId] = user.UserName
	f.ips[user.UserId] = clientIP
	if cur, ok := f.best[user.UserId]; ok && cur.Compare(score) >= 0 {
		return false, cur, nil
	}
	f.best[user.UserId] = score
	return true, score, nil
}

func (f *fakeRankStorage) CountMembers(_ context.Context, refs []rankrepo.BoardRef) ([]int64, error) {
	return make([]int64, len(refs)), nil
}

func (f *fakeRankStorage) GetBoards(_ context.Context, refs []rankrepo.BoardRef, userId string, _ int) ([]*rankentity.RankResult, error) {
	f.gotUserId = userId
	out := make([]*rankentity.RankResult, len(refs))
	for i, r := range refs {
		if i < len(f.boards) {
			out[i] = f.boards[i]
			continue
		}
		out[i] = rankentity.NewRankResult("", r.Level)
		out[i].Window = r.Window
	}
	return out, nil
}

func (f *fakeRankStorage) GetMemberBests(_ context.Context, _ string, _ []string) ([]*vo.ScoreMeta, error) {
	return nil, nil
}

type fakeRecordQueue struct {
	records []*recordentity.GameRecord
	err     error
}

func (q *fakeRecordQueue) Append(_ context.Context, r *recordentity.GameRecord) error {
	if q.err != nil {
		return q.err
	}
	q.records = append(q.records, r)
	return nil
}

type fakeAchievements struct {
	data map[string]*achievemententity.Achievement
	err  error
}

func (f *fakeAchievements) Unlock(_ context.Context, a *achievemententity.Achievement) (*achievemententity.Achievement, bool, error) {
	if f.err != nil {
		return nil, false, f.err
	}
	key := a.UserID + "/" + a.AchievementID
	if cur, ok := f.data[key]; ok {
		return cur, false, nil
	}
	f.data[key] = a
	return a, true, nil
}

type harness struct {
	router   *gin.Engine
	wechat   *persontest.FakeWechat
	players  *persontest.Players
	sessions *persontest.Sessions
	limiter  *persontest.Limiter
	friends  *persontest.Friends
	rank     *fakeRankStorage
	records  *fakeRecordQueue
	achieved *fakeAchievements
	runs     *runtest.Runs
	logs     *observer.ObservedLogs
}

func setup(t *testing.T, cfg *config.Config) *harness {
	t.Helper()
	gin.SetMode(gin.TestMode)
	core, logs := observer.New(zapcore.DebugLevel)
	util.SetLogger(zap.New(core))

	h := &harness{
		wechat:   persontest.NewFakeWechat(),
		players:  persontest.NewPlayers(),
		sessions: persontest.NewSessions(),
		limiter:  persontest.NewLimiter(),
		friends:  persontest.NewFriends(),
		rank:     &fakeRankStorage{},
		records:  &fakeRecordQueue{},
		achieved: &fakeAchievements{data: map[string]*achievemententity.Achievement{}},
		runs:     runtest.NewRuns(),
		logs:     logs,
	}
	h.wechat.OpenIDs["code-a"] = "openid-a"
	h.wechat.OpenIDs["code-a2"] = "openid-a"
	h.wechat.OpenIDs["code-b"] = "openid-b"
	personimpl.Init(h.wechat, h.players, h.sessions, h.limiter, h.friends, personimpl.Options{
		AppID: testAppID, SessionTTL: time.Hour, LoginRateLimit: 5, LoginRateWindow: time.Minute,
		FriendTTL: time.Hour, MaxFriends: 100,
	})
	impl.Init(h.rank, 100, 10)
	recordimpl.Init(h.records)
	achievementimpl.Init(h.achieved)
	runimpl.Init(h.runs, runtest.NewLimiter(), runimpl.Options{TicketTTL: time.Hour, StartLimit: 100, StartWindow: time.Hour})
	processor.Init(100_000, false)
	if cfg == nil {
		cfg = &config.Config{}
	}
	h.router = newRouter(cfg)
	return h
}

// tryStartRun 领取一张普通局对局票,失败(如 token 无效)时返回空串。
func (h *harness) tryStartRun(token string) string {
	req := httptest.NewRequest(http.MethodPost, "/runs/start", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	var env struct {
		Data struct {
			RunId string `json:"run_id"`
		} `json:"data"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &env) != nil {
		return ""
	}
	return env.Data.RunId
}

func (h *harness) do(t *testing.T, method, path, token, body string, headers ...string) (*httptest.ResponseRecorder, map[string]interface{}) {
	t.Helper()
	// 和真实客户端一样:未指定 run_id 的成绩上报先开一局普通局(规则本身由 TestRun* 显式覆盖)
	if path == "/upload_score" && token != "" && strings.HasPrefix(body, "{") && !strings.Contains(body, `"run_id"`) {
		runField := `"run_id":"` + h.tryStartRun(token) + `"`
		if body == "{}" {
			body = "{" + runField + "}"
		} else {
			body = "{" + runField + "," + body[1:]
		}
	}
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	envelope := map[string]interface{}{}
	if w.Body.Len() > 0 {
		if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("response is not JSON: %v (%s)", err, w.Body.String())
		}
	}
	return w, envelope
}

// login 登录并返回 token 与 user_id。
func (h *harness) login(t *testing.T, code string) (string, string) {
	t.Helper()
	w, env := h.do(t, http.MethodPost, "/auth/wechat", "", fmt.Sprintf(`{"code":%q}`, code))
	if w.Code != http.StatusOK {
		t.Fatalf("login(%s) status = %d body = %s", code, w.Code, w.Body.String())
	}
	data := env["data"].(map[string]interface{})
	user := data["user"].(map[string]interface{})
	return data["token"].(string), user["user_id"].(string)
}

func assertEnvelope(t *testing.T, w *httptest.ResponseRecorder, env map[string]interface{}, status int) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("http status = %d, want %d (body %s)", w.Code, status, w.Body.String())
	}
	if code, _ := env["code"].(float64); int(code) != status {
		t.Errorf("envelope code = %v, want %d", env["code"], status)
	}
}

const uploadBody = `{"location":{"province":"浙江省","city":"杭州市","district":"西湖区"},"stage":12,"stage_cultivation":3400,"total_cultivation":98000}`

// board 取 fetch_rank 响应中某类榜单的某个窗口,例如 board(t, env, "global", "all")。
func board(t *testing.T, env map[string]interface{}, kind, window string) map[string]interface{} {
	t.Helper()
	set, ok := env["data"].(map[string]interface{})[kind].(map[string]interface{})
	if !ok {
		t.Fatalf("%s boards missing: %v", kind, env["data"])
	}
	b, ok := set[window].(map[string]interface{})
	if !ok {
		t.Fatalf("%s.%s missing: %v", kind, window, set)
	}
	return b
}

func entries(b map[string]interface{}) []map[string]interface{} {
	var out []map[string]interface{}
	for _, e := range b["score_meta_list"].([]interface{}) {
		out = append(out, e.(map[string]interface{}))
	}
	return out
}

func TestWechatLoginSuccess(t *testing.T) {
	h := setup(t, nil)
	w, env := h.do(t, http.MethodPost, "/auth/wechat", "", `{"code":"code-a"}`)
	assertEnvelope(t, w, env, http.StatusOK)

	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	data := env["data"].(map[string]interface{})
	user := data["user"].(map[string]interface{})
	if user["user_id"] != entity.PlayerID(testAppID, "openid-a") || user["nickname"] != "无名" || user["avatar_url"] != "" {
		t.Errorf("user = %v", user)
	}
	if tok, _ := data["token"].(string); len(tok) < 43 {
		t.Errorf("token = %q", tok)
	}
	if exp, _ := data["expires_at"].(float64); int64(exp) <= time.Now().Unix() {
		t.Errorf("expires_at = %v, want future unix seconds", data["expires_at"])
	}
	body := w.Body.String()
	for _, leak := range []string{"openid", "session_key", "appid"} {
		if strings.Contains(body, leak) {
			t.Errorf("login response leaks %q: %s", leak, body)
		}
	}
}

func TestRepeatedLoginSameUserAcrossDevices(t *testing.T) {
	h := setup(t, nil)
	tokA1, idA1 := h.login(t, "code-a")
	tokA2, idA2 := h.login(t, "code-a2")
	_, idB := h.login(t, "code-b")
	if idA1 != idA2 || idA1 == idB {
		t.Fatalf("ids: a1=%s a2=%s b=%s", idA1, idA2, idB)
	}
	for _, tok := range []string{tokA1, tokA2} {
		w, env := h.do(t, http.MethodGet, "/users/me", tok, "")
		assertEnvelope(t, w, env, http.StatusOK)
	}
}

func TestLoginErrorMapping(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		err    error
		status int
	}{
		{"missing code", `{}`, nil, http.StatusBadRequest},
		{"malformed json", `{"code":`, nil, http.StatusBadRequest},
		{"unknown code", `{"code":"bogus"}`, nil, http.StatusUnauthorized},
		{"risk blocked", `{"code":"code-a"}`, &provider.UpstreamError{Kind: provider.ErrRiskBlocked, ErrCode: 40226}, http.StatusForbidden},
		{"wechat rate limit", `{"code":"code-a"}`, &provider.UpstreamError{Kind: provider.ErrRateLimited, ErrCode: 45011}, http.StatusTooManyRequests},
		{"bad secret", `{"code":"code-a"}`, &provider.UpstreamError{Kind: provider.ErrUpstream, ErrCode: 40125}, http.StatusBadGateway},
		{"timeout", `{"code":"code-a"}`, &provider.UpstreamError{Kind: provider.ErrUpstreamTimeout}, http.StatusGatewayTimeout},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := setup(t, nil)
			h.wechat.Err = tt.err
			w, env := h.do(t, http.MethodPost, "/auth/wechat", "", tt.body)
			assertEnvelope(t, w, env, tt.status)
			if env["data"] != nil {
				t.Errorf("data = %v, want null on error", env["data"])
			}
			if strings.Contains(env["msg"].(string), "errcode") {
				t.Errorf("msg leaks upstream detail: %q", env["msg"])
			}
		})
	}
}

func TestLoginStorageDownReturns503WithoutToken(t *testing.T) {
	h := setup(t, nil)
	h.sessions.Err = errors.New("dial tcp 127.0.0.1:6379: connection refused")
	w, env := h.do(t, http.MethodPost, "/auth/wechat", "", `{"code":"code-a"}`)
	assertEnvelope(t, w, env, http.StatusServiceUnavailable)
	if env["msg"] != "service unavailable" || env["data"] != nil {
		t.Errorf("envelope = %v", env)
	}
}

func TestLoginRateLimitUsesDirectClientIP(t *testing.T) {
	h := setup(t, nil)
	for i := 0; i < 5; i++ {
		// 未配置可信代理时,伪造的 X-Forwarded-For 不能绕开限流
		w, env := h.do(t, http.MethodPost, "/auth/wechat", "", `{"code":"code-a"}`, "X-Forwarded-For", fmt.Sprintf("10.0.0.%d", i))
		assertEnvelope(t, w, env, http.StatusOK)
	}
	w, env := h.do(t, http.MethodPost, "/auth/wechat", "", `{"code":"code-a"}`, "X-Forwarded-For", "10.9.9.9")
	assertEnvelope(t, w, env, http.StatusTooManyRequests)
	if len(h.limiter.Counts) != 1 {
		t.Errorf("limiter keys = %v, want a single direct-IP key", h.limiter.Counts)
	}
}

func TestLoginRateLimitHonorsConfiguredProxy(t *testing.T) {
	h := setup(t, &config.Config{TrustedProxies: []string{"192.0.2.0/24"}})
	h.do(t, http.MethodPost, "/auth/wechat", "", `{"code":"code-a"}`, "X-Forwarded-For", "203.0.113.7")
	if _, ok := h.limiter.Counts["login:203.0.113.7"]; !ok {
		t.Errorf("limiter keys = %v, want forwarded client IP from trusted proxy", h.limiter.Counts)
	}
}

func TestProtectedEndpointsRequireToken(t *testing.T) {
	h := setup(t, nil)
	routes := []struct{ method, path, body string }{
		{http.MethodPost, "/upload_score", uploadBody},
		{http.MethodPost, "/fetch_rank", `{}`},
		{http.MethodGet, "/users/me", ""},
		{http.MethodPatch, "/users/me/profile", `{"nickname":"x"}`},
		{http.MethodPost, "/auth/logout", ""},
		{http.MethodPost, "/upload_game_record", `{"stage":1}`},
		{http.MethodPost, "/upload_achievement", `{"achievement_id":"a"}`},
		{http.MethodPost, "/runs/start", ""},
	}
	for _, r := range routes {
		for name, tok := range map[string]string{"missing": "", "forged": "forged-token"} {
			w, env := h.do(t, r.method, r.path, tok, r.body)
			if w.Code != http.StatusUnauthorized || env["code"].(float64) != 401 {
				t.Errorf("%s %s with %s token: status %d", r.method, r.path, name, w.Code)
			}
		}
	}
	if len(h.rank.best) != 0 || len(h.records.records) != 0 {
		t.Error("unauthenticated requests must not reach storage")
	}

	req := httptest.NewRequest(http.MethodGet, "/users/me", nil)
	req.Header.Set("Authorization", "Basic abc")
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("non-bearer scheme status = %d, want 401", w.Code)
	}
}

func TestUploadIgnoresForgedUserIdInBody(t *testing.T) {
	h := setup(t, nil)
	tokA, idA := h.login(t, "code-a")
	_, idB := h.login(t, "code-b")

	forged := fmt.Sprintf(`{"user_id":%q,"user_name":"冒充者","location":{"province":"浙江省"},"stage":50,"stage_cultivation":0,"total_cultivation":0}`, idB)
	w, env := h.do(t, http.MethodPost, "/upload_score", tokA, forged)
	assertEnvelope(t, w, env, http.StatusOK)

	if _, ok := h.rank.best[idB]; ok {
		t.Fatal("A's token must not write a score for B")
	}
	if h.rank.best[idA].Stage != 50 || h.rank.names[idA] != "无名" || h.rank.ips[idA] != "192.0.2.1" {
		t.Errorf("score stored as %v / name %q, want A with server nickname", h.rank.best, h.rank.names[idA])
	}

	w, env = h.do(t, http.MethodPost, "/fetch_rank", tokA,
		fmt.Sprintf(`{"user_id":%q,"location":{"province":"浙江省"}}`, idB))
	assertEnvelope(t, w, env, http.StatusOK)
	if h.rank.gotUserId != idA {
		t.Errorf("self rank computed for %s, want %s", h.rank.gotUserId, idA)
	}
}

func TestUploadScoreValidationAndEnvelope(t *testing.T) {
	h := setup(t, nil)
	tok, _ := h.login(t, "code-a")

	w, env := h.do(t, http.MethodPost, "/upload_score", tok, uploadBody)
	assertEnvelope(t, w, env, http.StatusOK)
	data := env["data"].(map[string]interface{})
	best, _ := data["best"].(map[string]interface{})
	if data["updated"] != true || best["stage"].(float64) != 12 || best["stage_cultivation"] != "3400" ||
		best["total_cultivation"] != "98000" {
		t.Errorf("data = %v (cultivation must be returned as decimal strings)", data)
	}
	// 超过安全整数的修为必须以字符串上报
	w, env = h.do(t, http.MethodPost, "/upload_score", tok, `{"stage":40,"stage_cultivation":"9.31009731811524e+23","total_cultivation":"123456789012345678901234567890"}`)
	assertEnvelope(t, w, env, http.StatusOK)
	if best := env["data"].(map[string]interface{})["best"].(map[string]interface{}); best["stage_cultivation"] != "931009731811524000000000" ||
		best["total_cultivation"] != "123456789012345678901234567890" {
		t.Errorf("big best = %v", best)
	}
	w, env = h.do(t, http.MethodPost, "/upload_score", tok, `{"stage":41,"stage_cultivation":1,"total_cultivation":9007199254740993}`)
	assertEnvelope(t, w, env, http.StatusBadRequest)

	w, env = h.do(t, http.MethodPost, "/upload_score", tok, strings.Replace(uploadBody, `"stage":12`, `"stage":0`, 1))
	assertEnvelope(t, w, env, http.StatusBadRequest)
	// 小数不是合法的整数成绩
	w, env = h.do(t, http.MethodPost, "/upload_score", tok, strings.Replace(uploadBody, `"stage":12`, `"stage":1.5`, 1))
	assertEnvelope(t, w, env, http.StatusBadRequest)
	if env["msg"].(string) == "" {
		t.Error("msg should describe the validation failure")
	}

	w, env = h.do(t, http.MethodPost, "/upload_score", tok, `{"location":`)
	assertEnvelope(t, w, env, http.StatusBadRequest)
}

func TestFetchRankEnvelopeAndPayload(t *testing.T) {
	h := setup(t, nil)
	tok, id := h.login(t, "code-a")
	global := rankentity.NewRankResult("", vo.Nation)
	global.Window = rankentity.WindowAll
	global.SelfRank = 1
	global.SelfScore = vo.Score{Stage: 12, StageCultivation: "3400", TotalCultivation: "98000"}
	global.ScoreMetaList = []*vo.ScoreMeta{
		{UserInfo: &vo.UserInfo{UserId: id, UserName: "阿甘"}, Score: global.SelfScore, CreateTime: 123},
	}
	h.rank.boards = []*rankentity.RankResult{global}

	w, env := h.do(t, http.MethodPost, "/fetch_rank", tok, `{"location":{"province":"浙江省","city":"杭州市"}}`)
	assertEnvelope(t, w, env, http.StatusOK)
	data := env["data"].(map[string]interface{})
	if hidden, ok := data["hidden"]; !ok || hidden != false {
		t.Errorf("hidden = %v (present=%v), want explicit false", hidden, ok)
	}
	if data["friend"] != nil {
		t.Errorf("friend must be null before /friends/sync: %v", data["friend"])
	}
	all := board(t, env, "global", "all")
	if all["scope"] != "global" || all["window"] != "all" || all["self_rank"].(float64) != 1 {
		t.Errorf("global all = %v", all)
	}
	if self := all["self_score"].(map[string]interface{}); self["stage"].(float64) != 12 {
		t.Errorf("self_score = %v", self)
	}
	e := entries(all)[0]
	// 榜单里存的是旧名,展示以玩家当前资料为准(尚未授权 → 无名、空头像)
	if e["rank"].(float64) != 1 || e["user_id"] != id || e["user_name"] != "无名" || e["avatar_url"] != "" ||
		e["stage"].(float64) != 12 || e["stage_cultivation"] != "3400" || e["total_cultivation"] != "98000" {
		t.Errorf("entry = %v", e)
	}
	// 城市人数不足(fake 计数为 0)→ 地榜退回省份
	if region := board(t, env, "region", "week"); region["scope"] != "province" || region["self_score"] != nil {
		t.Errorf("region week = %v", region)
	}
	if week := board(t, env, "global", "week"); week["self_rank"].(float64) != 0 || week["self_score"] != nil {
		t.Errorf("empty global week = %v", week)
	}
}

func TestFriendSyncAndFriendBoardFlow(t *testing.T) {
	h := setup(t, nil)
	tokA, idA := h.login(t, "code-a")
	h.wechat.Friends["openid-a"] = []string{"openid-b", "openid-c"}

	body := `{"code":"code-a2","encrypted_data":"ZW5j","iv":"aXY="}`
	w, env := h.do(t, http.MethodPost, "/friends/sync", tokA, body)
	assertEnvelope(t, w, env, http.StatusOK)
	if n := env["data"].(map[string]interface{})["friend_count"].(float64); n != 2 {
		t.Errorf("friend_count = %v, want 2", n)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Error("friend sync response must be no-store")
	}
	if ids := h.friends.Data[idA]; len(ids) != 2 {
		t.Errorf("stored friends = %v", ids)
	}

	// code 属于另一个微信号 → 400,不覆盖已有好友
	w, env = h.do(t, http.MethodPost, "/friends/sync", tokA, `{"code":"code-b","encrypted_data":"ZW5j","iv":"aXY="}`)
	assertEnvelope(t, w, env, http.StatusBadRequest)
	if len(h.friends.Data[idA]) != 2 {
		t.Error("mismatched sync must not change friends")
	}
	// 解密失败 → 400
	h.wechat.FriendErr = &provider.UpstreamError{Kind: provider.ErrInvalidEncryptedData}
	w, env = h.do(t, http.MethodPost, "/friends/sync", tokA, body)
	assertEnvelope(t, w, env, http.StatusBadRequest)
	h.wechat.FriendErr = nil
	// 过期 code → 401,提示是 code 问题而不是 token 问题
	w, env = h.do(t, http.MethodPost, "/friends/sync", tokA, `{"code":"stale","encrypted_data":"ZW5j","iv":"aXY="}`)
	assertEnvelope(t, w, env, http.StatusUnauthorized)
	if env["msg"] != "invalid or expired login code" {
		t.Errorf("msg = %v", env["msg"])
	}

	// 同步后 fetch_rank 返回人榜
	w, env = h.do(t, http.MethodPost, "/fetch_rank", tokA, `{}`)
	assertEnvelope(t, w, env, http.StatusOK)
	if b := board(t, env, "friend", "all"); b["scope"] != "friend" {
		t.Errorf("friend board = %v", b)
	}
	if env["data"].(map[string]interface{})["region"] != nil {
		t.Error("region must be null without location")
	}
}

func TestFriendSyncBodyLimitIsLarger(t *testing.T) {
	h := setup(t, nil)
	tok, _ := h.login(t, "code-a")
	h.wechat.Friends["openid-a"] = []string{"openid-b"}
	// 大于普通接口的 16 KiB 上限,但在好友同步的上限内
	big := `{"code":"code-a2","encrypted_data":"` + strings.Repeat("A", 100<<10) + `","iv":"aXY="}`
	w, env := h.do(t, http.MethodPost, "/friends/sync", tok, big)
	assertEnvelope(t, w, env, http.StatusOK)
	huge := `{"code":"code-a2","encrypted_data":"` + strings.Repeat("A", maxFriendSyncBodyBytes) + `","iv":"aXY="}`
	w, env = h.do(t, http.MethodPost, "/friends/sync", tok, huge)
	assertEnvelope(t, w, env, http.StatusRequestEntityTooLarge)
}

func TestProfileUpdateFlow(t *testing.T) {
	h := setup(t, nil)
	tok, id := h.login(t, "code-a")

	w, env := h.do(t, http.MethodPatch, "/users/me/profile", tok,
		`{"nickname":"阿甘🎴","avatar_url":"https://thirdwx.qlogo.cn/mmopen/a/132"}`)
	assertEnvelope(t, w, env, http.StatusOK)
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Error("profile response must be no-store")
	}
	user := env["data"].(map[string]interface{})
	if user["user_id"] != id || user["nickname"] != "阿甘🎴" || user["avatar_url"] != "https://thirdwx.qlogo.cn/mmopen/a/132" {
		t.Errorf("updated user = %v", user)
	}

	w, env = h.do(t, http.MethodGet, "/users/me", tok, "")
	assertEnvelope(t, w, env, http.StatusOK)
	if env["data"].(map[string]interface{})["nickname"] != "阿甘🎴" {
		t.Errorf("/users/me = %v", env["data"])
	}

	// 重新登录不覆盖已授权资料;后续成绩使用服务端昵称
	tok2, _ := h.login(t, "code-a2")
	h.do(t, http.MethodPost, "/upload_score", tok2, uploadBody)
	if h.rank.names[id] != "阿甘🎴" {
		t.Errorf("rank name = %q, want server nickname", h.rank.names[id])
	}

	for name, body := range map[string]string{
		"empty nickname": `{"nickname":"","avatar_url":""}`,
		"http avatar":    `{"nickname":"x","avatar_url":"http://thirdwx.qlogo.cn/a"}`,
		"too long":       fmt.Sprintf(`{"nickname":%q}`, strings.Repeat("名", 65)),
	} {
		w, env := h.do(t, http.MethodPatch, "/users/me/profile", tok, body)
		if w.Code != http.StatusBadRequest || env["code"].(float64) != 400 {
			t.Errorf("%s: status = %d", name, w.Code)
		}
	}
}

func TestLogoutRevokesOnlyCurrentToken(t *testing.T) {
	h := setup(t, nil)
	tok1, _ := h.login(t, "code-a")
	tok2, _ := h.login(t, "code-a2")

	w, env := h.do(t, http.MethodPost, "/auth/logout", tok1, "")
	assertEnvelope(t, w, env, http.StatusOK)
	w, env = h.do(t, http.MethodGet, "/users/me", tok1, "")
	assertEnvelope(t, w, env, http.StatusUnauthorized)
	w, env = h.do(t, http.MethodGet, "/users/me", tok2, "")
	assertEnvelope(t, w, env, http.StatusOK)
}

func TestRequestBodyLimit(t *testing.T) {
	h := setup(t, nil)
	big := `{"code":"` + strings.Repeat("a", maxRequestBodyBytes) + `"}`
	w, env := h.do(t, http.MethodPost, "/auth/wechat", "", big)
	assertEnvelope(t, w, env, http.StatusRequestEntityTooLarge)
	if h.wechat.Calls != 0 {
		t.Error("oversized request must not reach wechat")
	}
}

func TestCORSPreflightWhitelist(t *testing.T) {
	h := setup(t, &config.Config{CORSAllowOrigins: []string{"http://localhost:7456"}})

	req := httptest.NewRequest(http.MethodOptions, "/users/me/profile", nil)
	req.Header.Set("Origin", "http://localhost:7456")
	req.Header.Set("Access-Control-Request-Method", "PATCH")
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent || w.Header().Get("Access-Control-Allow-Origin") != "http://localhost:7456" ||
		!strings.Contains(w.Header().Get("Access-Control-Allow-Headers"), "Authorization") {
		t.Errorf("allowed preflight: status %d headers %v", w.Code, w.Header())
	}

	req = httptest.NewRequest(http.MethodOptions, "/users/me/profile", nil)
	req.Header.Set("Origin", "https://evil.example")
	w = httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("unlisted origin got CORS headers: %v", w.Header())
	}
}

func TestLogsContainNoSecretsOrProfileText(t *testing.T) {
	h := setup(t, nil)
	h.wechat.OpenIDs["secret-login-code"] = "openid-secret"
	tok, _ := h.login(t, "secret-login-code")
	h.do(t, http.MethodPatch, "/users/me/profile", tok, `{"nickname":"隐私昵称","avatar_url":"https://thirdwx.qlogo.cn/private"}`)
	h.do(t, http.MethodPost, "/upload_score", tok, uploadBody)
	h.do(t, http.MethodGet, "/users/me", "bogus-token-value", "")
	h.wechat.Err = &provider.UpstreamError{Kind: provider.ErrUpstream, ErrCode: 40125}
	h.do(t, http.MethodPost, "/auth/wechat", "", `{"code":"secret-login-code"}`)

	entries := h.logs.All()
	if len(entries) == 0 {
		t.Fatal("expected request logs")
	}
	var dump strings.Builder
	for _, e := range entries {
		dump.WriteString(e.Message)
		for k, v := range e.ContextMap() {
			fmt.Fprintf(&dump, " %s=%v", k, v)
		}
		dump.WriteString("\n")
	}
	logText := dump.String()
	for _, secret := range []string{
		"secret-login-code", tok, "bogus-token-value", "openid-secret", "Bearer",
		"隐私昵称", "thirdwx.qlogo.cn/private", "浙江省",
	} {
		if strings.Contains(logText, secret) {
			t.Errorf("logs leak %q:\n%s", secret, logText)
		}
	}
	if !strings.Contains(logText, "errcode=40125") {
		t.Errorf("upstream failure should be logged with its errcode:\n%s", logText)
	}
}

// 集成测试:真实 Redis 存储 + 假微信,验证完整链路;Redis 不可达时跳过。
// 使用 DB 14:repository_impl 包的测试并行地清空 DB 15。
func TestEndToEndWithRedis(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379", DB: 14})
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("redis not available: %v", err)
	}
	if err := client.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	t.Cleanup(func() {
		_ = client.FlushDB(context.Background()).Err()
		_ = client.Close()
	})

	h := setup(t, nil)
	personimpl.Init(h.wechat,
		repository_impl.NewPlayerRedisStorage(client),
		repository_impl.NewSessionRedisStorage(client),
		repository_impl.NewRateLimiterRedis(client),
		repository_impl.NewFriendRedisStorage(client),
		personimpl.Options{AppID: testAppID, SessionTTL: time.Hour, LoginRateLimit: 5, LoginRateWindow: time.Minute,
			FriendTTL: time.Hour, MaxFriends: 100})
	impl.Init(repository_impl.NewRankRedisStorage(client), 100, 1)
	runimpl.Init(repository_impl.NewRunRedisStorage(client), repository_impl.NewRateLimiterRedis(client),
		runimpl.Options{TicketTTL: time.Hour, StartLimit: 100, StartWindow: time.Hour})

	tok, id := h.login(t, "code-a")
	if n := client.Exists(ctx, "auth:session:"+tok).Val(); n != 0 {
		t.Error("plaintext token must not be a redis key")
	}
	w, env := h.do(t, http.MethodPatch, "/users/me/profile", tok, `{"nickname":"阿甘🎴"}`)
	assertEnvelope(t, w, env, http.StatusOK)
	w, env = h.do(t, http.MethodPost, "/upload_score", tok, uploadBody)
	assertEnvelope(t, w, env, http.StatusOK)

	w, env = h.do(t, http.MethodPost, "/fetch_rank", tok, `{"location":{"province":"浙江省","city":"杭州市"}}`)
	assertEnvelope(t, w, env, http.StatusOK)
	top := board(t, env, "global", "all")
	if e := entries(top)[0]; e["user_id"] != id || e["user_name"] != "阿甘🎴" || e["stage"].(float64) != 12 || top["self_rank"].(float64) != 1 {
		t.Errorf("global top = %v", top)
	}
	// 杭州已有 1 人,达到阈值 1 → 地榜为城市榜
	if r := board(t, env, "region", "all"); r["scope"] != "city" || r["address"] != "杭州市" || r["self_rank"].(float64) != 1 {
		t.Errorf("region all = %v", r)
	}
	if rev := client.HGet(ctx, "player:dirty", id).Val(); rev != "2" {
		t.Errorf("player dirty revision = %q, want 2 (login + profile)", rev)
	}

	tokB, idB := h.login(t, "code-b")
	// B 关卡相同但最后一关修为更高 → 排在 A 前面
	w, env = h.do(t, http.MethodPost, "/upload_score", tokB,
		`{"location":{"province":"上海市"},"stage":12,"stage_cultivation":3401,"total_cultivation":3401,"profile":{"nickname":"小B","avatar_url":"https://thirdwx.qlogo.cn/b/132"}}`)
	assertEnvelope(t, w, env, http.StatusOK)
	w, env = h.do(t, http.MethodPost, "/fetch_rank", tok, `{"location":{"province":"浙江省","city":"杭州市"}}`)
	assertEnvelope(t, w, env, http.StatusOK)
	top = board(t, env, "global", "all")
	first := entries(top)[0]
	if first["user_id"] != idB || first["user_name"] != "小B" || first["avatar_url"] != "https://thirdwx.qlogo.cn/b/132" {
		t.Errorf("B's entry = %v, want nickname and avatar from upload profile", first)
	}
	if top["self_rank"].(float64) != 2 {
		t.Errorf("A self_rank = %v, want 2", top["self_rank"])
	}

	// B 改资料后,无需重新上传,榜单立即显示新昵称
	h.do(t, http.MethodPatch, "/users/me/profile", tokB, `{"nickname":"大B","avatar_url":""}`)
	_, env = h.do(t, http.MethodPost, "/fetch_rank", tok, `{"location":{"province":"浙江省","city":"杭州市"}}`)
	first = entries(board(t, env, "global", "all"))[0]
	if first["user_name"] != "大B" || first["avatar_url"] != "" {
		t.Errorf("B's entry after profile change = %v", first)
	}
	// B 不在浙江,地榜只有 A
	if r := entries(board(t, env, "region", "all")); len(r) != 1 || r[0]["user_id"] != id {
		t.Errorf("region entries = %v", r)
	}

	// 人榜:A 同步好友 B 后,好友榜按成绩排序
	h.wechat.Friends["openid-a"] = []string{"openid-b"}
	w, env = h.do(t, http.MethodPost, "/friends/sync", tok, `{"code":"code-a2","encrypted_data":"ZW5j","iv":"aXY="}`)
	assertEnvelope(t, w, env, http.StatusOK)
	_, env = h.do(t, http.MethodPost, "/fetch_rank", tok, `{"location":{"province":"浙江省","city":"杭州市"}}`)
	friend := board(t, env, "friend", "week")
	if fe := entries(friend); len(fe) != 2 || fe[0]["user_id"] != idB || fe[1]["user_id"] != id || friend["self_rank"].(float64) != 2 {
		t.Errorf("friend week = %v", friend)
	}

	achievementimpl.Init(repository_impl.NewAchievementRedisStorage(client, 100))
	w, env = h.do(t, http.MethodPost, "/upload_achievement", tok, `{"achievement_id":"first_ascension","detail":{"stage":19}}`)
	assertEnvelope(t, w, env, http.StatusOK)
	w, env = h.do(t, http.MethodPost, "/upload_achievement", tok, `{"achievement_id":"first_ascension"}`)
	assertEnvelope(t, w, env, http.StatusOK)
	if env["data"].(map[string]interface{})["newly_unlocked"] != false {
		t.Errorf("repeat unlock = %v", env["data"])
	}
	if q := client.LRange(ctx, "achievement:queue", 0, -1).Val(); len(q) != 1 || !strings.Contains(q[0], `"user_id":"`+id+`"`) ||
		!strings.Contains(q[0], `"detail":{"stage":19}`) {
		t.Errorf("achievement queue = %v", q)
	}

	recordimpl.Init(repository_impl.NewGameRecordRedisQueue(client, 100))
	w, env = h.do(t, http.MethodPost, "/upload_game_record", tok, `{"stage":3,"result":"win"}`)
	assertEnvelope(t, w, env, http.StatusOK)
	queued := client.LRange(ctx, "game_record:queue", 0, -1).Val()
	if len(queued) != 1 || !strings.Contains(queued[0], `"user_id":"`+id+`"`) ||
		!strings.Contains(queued[0], `"data":{"stage":3,"result":"win"}`) {
		t.Errorf("queued records = %v", queued)
	}

	w, env = h.do(t, http.MethodPost, "/auth/logout", tok, "")
	assertEnvelope(t, w, env, http.StatusOK)
	w, env = h.do(t, http.MethodGet, "/users/me", tok, "")
	assertEnvelope(t, w, env, http.StatusUnauthorized)
}

func TestUploadWithInvalidProfileReturns400(t *testing.T) {
	h := setup(t, nil)
	tok, id := h.login(t, "code-a")
	w, env := h.do(t, http.MethodPost, "/upload_score", tok,
		`{"location":{"province":"浙江省"},"stage":1,"stage_cultivation":0,"total_cultivation":0,"profile":{"nickname":"","avatar_url":""}}`)
	assertEnvelope(t, w, env, http.StatusBadRequest)
	if _, ok := h.rank.best[id]; ok {
		t.Error("score must not be stored when profile is invalid")
	}
}

func TestUploadGameRecord(t *testing.T) {
	h := setup(t, nil)
	tok, id := h.login(t, "code-a")

	body := "{\n  \"user_id\": \"wx_someone_else\",\n  \"stage\": 3,\n  \"cards\": [\"A\", \"K\"]\n}"
	w, env := h.do(t, http.MethodPost, "/upload_game_record", tok, body)
	assertEnvelope(t, w, env, http.StatusOK)
	data := env["data"].(map[string]interface{})
	recordID, _ := data["record_id"].(string)
	if len(recordID) != 32 {
		t.Errorf("record_id = %q", recordID)
	}
	if at, _ := data["created_at_ms"].(float64); int64(at) < time.Now().Add(-time.Minute).UnixMilli() {
		t.Errorf("created_at_ms = %v", data["created_at_ms"])
	}

	if len(h.records.records) != 1 {
		t.Fatalf("queued = %d, want 1", len(h.records.records))
	}
	r := h.records.records[0]
	// 归属取自 token;body 里的 user_id 只是普通数据
	if r.UserID != id || r.ID != recordID || r.ClientIP != "192.0.2.1" {
		t.Errorf("record owner/id = %s/%s, want %s/%s", r.UserID, r.ID, id, recordID)
	}
	if string(r.Data) != `{"user_id":"wx_someone_else","stage":3,"cards":["A","K"]}` {
		t.Errorf("stored data = %s", r.Data)
	}

	// 同一玩家可以有多条记录
	h.do(t, http.MethodPost, "/upload_game_record", tok, `{"stage":4}`)
	if len(h.records.records) != 2 || h.records.records[1].ID == recordID {
		t.Errorf("second record not appended separately: %+v", h.records.records)
	}
}

func TestUploadGameRecordErrors(t *testing.T) {
	h := setup(t, nil)
	tok, _ := h.login(t, "code-a")

	for name, body := range map[string]string{
		"array":   `[1,2,3]`,
		"empty":   ``,
		"invalid": `{"stage":`,
	} {
		w, env := h.do(t, http.MethodPost, "/upload_game_record", tok, body)
		if w.Code != http.StatusBadRequest || env["code"].(float64) != 400 {
			t.Errorf("%s: status = %d", name, w.Code)
		}
	}

	big := `{"pad":"` + strings.Repeat("a", maxRequestBodyBytes) + `"}`
	w, env := h.do(t, http.MethodPost, "/upload_game_record", tok, big)
	assertEnvelope(t, w, env, http.StatusRequestEntityTooLarge)

	h.records.err = recordrepo.ErrQueueFull
	w, env = h.do(t, http.MethodPost, "/upload_game_record", tok, `{"stage":1}`)
	assertEnvelope(t, w, env, http.StatusServiceUnavailable)
	if env["msg"] != "service unavailable" {
		t.Errorf("msg = %v", env["msg"])
	}
	if len(h.records.records) != 0 {
		t.Error("nothing should be queued on errors")
	}
}

func TestPanicIsLoggedAndReturns500(t *testing.T) {
	h := setup(t, nil)
	h.router.GET("/boom", func(*gin.Context) { panic("kaboom") })

	w, env := h.do(t, http.MethodGet, "/boom", "", "")
	assertEnvelope(t, w, env, http.StatusInternalServerError)
	if env["msg"] != "internal server error" {
		t.Errorf("msg = %v", env["msg"])
	}
	found := false
	for _, e := range h.logs.All() {
		if e.Message == "panic recovered" && e.Level == zapcore.ErrorLevel && e.ContextMap()["panic"] == "kaboom" {
			found = true
		}
	}
	if !found {
		t.Error("panic should be logged at error level")
	}
}

func TestUploadAchievement(t *testing.T) {
	h := setup(t, nil)
	tok, id := h.login(t, "code-a")

	body := `{"achievement_id":"daluo_10","unlocked_at_ms":` + fmt.Sprint(time.Now().Add(-time.Minute).UnixMilli()) +
		`,"detail":{"stage":37,"user_id":"wx_someone_else"}}`
	w, env := h.do(t, http.MethodPost, "/upload_achievement", tok, body)
	assertEnvelope(t, w, env, http.StatusOK)
	data := env["data"].(map[string]interface{})
	if data["achievement_id"] != "daluo_10" || data["newly_unlocked"] != true || data["unlocked_at_ms"].(float64) == 0 {
		t.Errorf("data = %v", data)
	}
	saved := h.achieved.data[id+"/daluo_10"]
	if saved == nil || saved.UserID != id || string(saved.Detail) != `{"stage":37,"user_id":"wx_someone_else"}` || saved.ClientIP != "192.0.2.1" {
		t.Fatalf("saved = %+v (owner must come from token)", saved)
	}
	first := data["unlocked_at_ms"]

	// 重复上传:200,返回首次解锁时间
	w, env = h.do(t, http.MethodPost, "/upload_achievement", tok, `{"achievement_id":"daluo_10"}`)
	assertEnvelope(t, w, env, http.StatusOK)
	data = env["data"].(map[string]interface{})
	if data["newly_unlocked"] != false || data["unlocked_at_ms"] != first {
		t.Errorf("repeat = %v", data)
	}
}

func TestUploadAchievementErrors(t *testing.T) {
	h := setup(t, nil)
	tok, _ := h.login(t, "code-a")
	for name, body := range map[string]string{
		"missing id":   `{}`,
		"bad id":       `{"achievement_id":"飞升"}`,
		"detail array": `{"achievement_id":"a","detail":[1]}`,
		"bad time":     `{"achievement_id":"a","unlocked_at_ms":123}`,
		"wrong type":   `{"achievement_id":123}`,
	} {
		w, env := h.do(t, http.MethodPost, "/upload_achievement", tok, body)
		if w.Code != http.StatusBadRequest || env["code"].(float64) != 400 {
			t.Errorf("%s: status = %d", name, w.Code)
		}
	}
	h.achieved.err = errors.New("redis down")
	w, env := h.do(t, http.MethodPost, "/upload_achievement", tok, `{"achievement_id":"a"}`)
	assertEnvelope(t, w, env, http.StatusServiceUnavailable)
	if len(h.achieved.data) != 0 {
		t.Error("nothing should be saved on errors")
	}
}

func TestRunStartAndTicketRules(t *testing.T) {
	h := setup(t, nil)
	tok, id := h.login(t, "code-a")
	tokB, _ := h.login(t, "code-b")

	w, env := h.do(t, http.MethodPost, "/runs/start", tok, "")
	assertEnvelope(t, w, env, http.StatusOK)
	run := env["data"].(map[string]interface{})
	seed, _ := run["seed"].(string)
	if len(seed) != 8 || len(run["run_id"].(string)) != 32 || run["expires_at_ms"].(float64) <= run["started_at_ms"].(float64) {
		t.Fatalf("run = %v", run)
	}
	runID := run["run_id"].(string)

	body := func(runID string, stage int) string {
		return fmt.Sprintf(`{"run_id":%q,"stage":%d,"stage_cultivation":"10","total_cultivation":"100"}`, runID, stage)
	}
	// 首次成功,相同成绩重试成功,换成绩 409
	w, env = h.do(t, http.MethodPost, "/upload_score", tok, body(runID, 5))
	assertEnvelope(t, w, env, http.StatusOK)
	if d := env["data"].(map[string]interface{}); d["updated"] != true {
		t.Errorf("upload = %v", d)
	}
	w, env = h.do(t, http.MethodPost, "/upload_score", tok, body(runID, 5))
	assertEnvelope(t, w, env, http.StatusOK)
	w, env = h.do(t, http.MethodPost, "/upload_score", tok, body(runID, 6))
	assertEnvelope(t, w, env, http.StatusConflict)

	// 别人的票、伪造的票、缺 run_id
	w, env = h.do(t, http.MethodPost, "/upload_score", tokB, body(runID, 5))
	assertEnvelope(t, w, env, http.StatusBadRequest)
	w, env = h.do(t, http.MethodPost, "/upload_score", tok, body(strings.Repeat("a", 32), 5))
	assertEnvelope(t, w, env, http.StatusBadRequest)
	w, env = h.do(t, http.MethodPost, "/upload_score", tok, `{"run_id":"","stage":5,"stage_cultivation":"1","total_cultivation":"1"}`)
	assertEnvelope(t, w, env, http.StatusBadRequest)
	if h.rank.best[id].Stage != 5 {
		t.Errorf("best stage = %d, want 5", h.rank.best[id].Stage)
	}
}

func TestRunStartRateLimit(t *testing.T) {
	h := setup(t, nil)
	tok, _ := h.login(t, "code-a")
	runimpl.Init(h.runs, runtest.NewLimiter(), runimpl.Options{TicketTTL: time.Hour, StartLimit: 2, StartWindow: time.Hour})
	for i := 0; i < 2; i++ {
		w, env := h.do(t, http.MethodPost, "/runs/start", tok, "")
		assertEnvelope(t, w, env, http.StatusOK)
	}
	w, env := h.do(t, http.MethodPost, "/runs/start", tok, "")
	assertEnvelope(t, w, env, http.StatusTooManyRequests)
}
