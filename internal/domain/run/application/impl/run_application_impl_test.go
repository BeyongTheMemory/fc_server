package impl

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"fc_server/internal/domain/run/application"
	"fc_server/internal/domain/run/runtest"
)

type fixture struct {
	app     *RunApplicationImpl
	runs    *runtest.Runs
	limiter *runtest.Limiter
	clock   time.Time
}

func newFixture(t *testing.T, minMsPerStage int64) *fixture {
	t.Helper()
	f := &fixture{runs: runtest.NewRuns(), limiter: runtest.NewLimiter(), clock: time.UnixMilli(1_800_000_000_000)}
	f.app = New(f.runs, f.limiter, Options{TicketTTL: 30 * 24 * time.Hour, StartLimit: 3, StartWindow: time.Hour, MinMsPerStage: minMsPerStage})
	f.app.now = func() time.Time { return f.clock }
	return f
}

func TestStartRankedRunGetsServerSeed(t *testing.T) {
	f := newFixture(t, 0)
	r1, err := f.app.Start(context.Background(), "wx_u")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	r2, _ := f.app.Start(context.Background(), "wx_u")
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(r1.ID) || r1.ID == r2.ID {
		t.Errorf("run ids = %q, %q", r1.ID, r2.ID)
	}
	if !regexp.MustCompile(`^[A-Za-z0-9]{8}$`).MatchString(r1.Seed) || r1.Seed == r2.Seed {
		t.Errorf("seeds = %q, %q, want distinct 8-char alphanumerics", r1.Seed, r2.Seed)
	}
	if r1.UserID != "wx_u" || r1.StartedAtMs != 1_800_000_000_000 ||
		r1.ExpiresAtMs != f.clock.Add(30*24*time.Hour).UnixMilli() {
		t.Errorf("run = %+v", r1)
	}
	if _, ok := f.runs.Data[r1.ID]; !ok {
		t.Error("run should be stored")
	}
}

func TestStartRateLimitAndStorageErrors(t *testing.T) {
	f := newFixture(t, 0)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := f.app.Start(ctx, "wx_u"); err != nil {
			t.Fatalf("start %d: %v", i, err)
		}
	}
	if _, err := f.app.Start(ctx, "wx_u"); !errors.Is(err, application.ErrTooManyRuns) {
		t.Errorf("4th start err = %v, want ErrTooManyRuns", err)
	}
	if _, err := f.app.Start(ctx, "wx_other"); err != nil {
		t.Errorf("other player should have own budget: %v", err)
	}
	f.limiter.Err = errors.New("redis down")
	if _, err := f.app.Start(ctx, "wx_x"); !errors.Is(err, application.ErrUnavailable) {
		t.Errorf("limiter failure err = %v", err)
	}
	f.limiter.Err = nil
	f.runs.Err = errors.New("redis down")
	if _, err := f.app.Start(ctx, "wx_y"); !errors.Is(err, application.ErrUnavailable) {
		t.Errorf("storage failure err = %v", err)
	}
}

func TestAuthorizeScore(t *testing.T) {
	f := newFixture(t, 0)
	ctx := context.Background()
	run, _ := f.app.Start(ctx, "wx_u")

	if err := f.app.AuthorizeScore(ctx, "wx_u", run.ID, 12, "fp-1"); err != nil {
		t.Fatalf("first submit: %v", err)
	}
	// 客户端重试相同成绩:成功
	if err := f.app.AuthorizeScore(ctx, "wx_u", run.ID, 12, "fp-1"); err != nil {
		t.Errorf("replay err = %v, want allowed", err)
	}
	// 同一局换一份成绩:冲突
	if err := f.app.AuthorizeScore(ctx, "wx_u", run.ID, 13, "fp-2"); !errors.Is(err, application.ErrRunConflict) {
		t.Errorf("different score err = %v, want ErrRunConflict", err)
	}
	// 别人的票、不存在的票:同一错误
	for _, tc := range []struct{ user, run string }{{"wx_other", run.ID}, {"wx_u", "nonexistent"}} {
		if err := f.app.AuthorizeScore(ctx, tc.user, tc.run, 1, "fp"); !errors.Is(err, application.ErrRunNotFound) {
			t.Errorf("%+v err = %v, want ErrRunNotFound", tc, err)
		}
	}
}

func TestAuthorizeScoreMinimumTime(t *testing.T) {
	f := newFixture(t, 10_000) // 每关至少 10 秒
	ctx := context.Background()
	run, _ := f.app.Start(ctx, "wx_u")

	f.clock = f.clock.Add(59 * time.Second)
	if err := f.app.AuthorizeScore(ctx, "wx_u", run.ID, 6, "fp"); !errors.Is(err, application.ErrRunTooFast) {
		t.Fatalf("6 stages in 59s err = %v, want ErrRunTooFast", err)
	}
	if _, claimed := f.runs.Submitted[run.ID]; claimed {
		t.Error("rejected score must not use up the run")
	}
	f.clock = f.clock.Add(time.Second)
	if err := f.app.AuthorizeScore(ctx, "wx_u", run.ID, 6, "fp"); err != nil {
		t.Errorf("6 stages in 60s err = %v, want allowed", err)
	}
}

func TestAuthorizeScoreStorageError(t *testing.T) {
	f := newFixture(t, 0)
	run, _ := f.app.Start(context.Background(), "wx_u")
	f.runs.Err = errors.New("redis down")
	if err := f.app.AuthorizeScore(context.Background(), "wx_u", run.ID, 1, "fp"); !errors.Is(err, application.ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}
