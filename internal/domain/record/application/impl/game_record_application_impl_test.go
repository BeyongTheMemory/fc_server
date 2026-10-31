package impl

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"fc_server/internal/domain/record/application"
	"fc_server/internal/domain/record/entity"
	"fc_server/internal/domain/record/repository"
)

type fakeQueue struct {
	records []*entity.GameRecord
	err     error
}

func (q *fakeQueue) Append(_ context.Context, r *entity.GameRecord) error {
	if q.err != nil {
		return q.err
	}
	q.records = append(q.records, r)
	return nil
}

func TestAppendAssignsIDAndTime(t *testing.T) {
	q := &fakeQueue{}
	app := New(q)
	app.now = func() time.Time { return time.UnixMilli(1_800_000_000_123) }

	r1, err := app.Append(context.Background(), "wx_u", []byte(`{"stage":3}`), "203.0.113.9")
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	r2, _ := app.Append(context.Background(), "wx_u", []byte(`{"stage":4}`), "203.0.113.9")
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(r1.ID) || r1.ID == r2.ID {
		t.Errorf("ids = %q, %q; want distinct 32-hex", r1.ID, r2.ID)
	}
	if r1.UserID != "wx_u" || string(r1.Data) != `{"stage":3}` || r1.CreatedAtMs != 1_800_000_000_123 || r1.ClientIP != "203.0.113.9" {
		t.Errorf("record = %+v", r1)
	}
	if len(q.records) != 2 {
		t.Errorf("queued = %d, want 2", len(q.records))
	}
}

func TestAppendQueueErrorsAreUnavailable(t *testing.T) {
	for _, qerr := range []error{repository.ErrQueueFull, errors.New("redis down")} {
		app := New(&fakeQueue{err: qerr})
		if _, err := app.Append(context.Background(), "wx_u", []byte(`{}`), "203.0.113.9"); !errors.Is(err, application.ErrUnavailable) {
			t.Errorf("queue err %v → %v, want ErrUnavailable", qerr, err)
		}
	}
}
