package impl

import (
	"context"
	"errors"
	"testing"
	"time"

	"fc_server/internal/domain/achievement/application"
	"fc_server/internal/domain/achievement/entity"
	"fc_server/internal/domain/achievement/repository"
)

type fakeRepo struct {
	data map[string]*entity.Achievement
	err  error
}

func (f *fakeRepo) Unlock(_ context.Context, a *entity.Achievement) (*entity.Achievement, bool, error) {
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

func TestUnlockDefaultsTimeAndIsIdempotent(t *testing.T) {
	repo := &fakeRepo{data: map[string]*entity.Achievement{}}
	app := New(repo)
	app.now = func() time.Time { return time.UnixMilli(1_800_000_000_000) }
	ctx := context.Background()

	a, created, err := app.Unlock(ctx, "wx_u", "first_ascension", 0, []byte(`{"stage":19}`), "203.0.113.9")
	if err != nil || !created || a.UnlockedAtMs != 1_800_000_000_000 || a.ReceivedAtMs != 1_800_000_000_000 || string(a.Detail) != `{"stage":19}` || a.ClientIP != "203.0.113.9" {
		t.Fatalf("first unlock = (%+v, %v, %v)", a, created, err)
	}

	app.now = func() time.Time { return time.UnixMilli(1_800_000_999_000) }
	again, created, err := app.Unlock(ctx, "wx_u", "first_ascension", 1_700_000_000_000, nil, "203.0.113.9")
	if err != nil || created || again.UnlockedAtMs != 1_800_000_000_000 {
		t.Errorf("repeat unlock = (%+v, %v, %v), want original record", again, created, err)
	}

	b, created, _ := app.Unlock(ctx, "wx_u", "daluo_10", 1_799_000_000_000, nil, "203.0.113.9")
	if !created || b.UnlockedAtMs != 1_799_000_000_000 {
		t.Errorf("client time should be kept: %+v", b)
	}
}

func TestUnlockStorageErrorsAreUnavailable(t *testing.T) {
	for _, e := range []error{repository.ErrQueueFull, errors.New("redis down")} {
		app := New(&fakeRepo{err: e})
		if _, _, err := app.Unlock(context.Background(), "wx_u", "a", 0, nil, "203.0.113.9"); !errors.Is(err, application.ErrUnavailable) {
			t.Errorf("repo err %v → %v, want ErrUnavailable", e, err)
		}
	}
}
