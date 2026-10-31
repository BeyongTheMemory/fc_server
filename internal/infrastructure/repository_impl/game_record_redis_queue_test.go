package repository_impl

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"fc_server/internal/domain/record/entity"
	"fc_server/internal/domain/record/repository"
)

func newTestRecordQueue(t *testing.T, maxLen int) *GameRecordRedisQueue {
	t.Helper()
	return NewGameRecordRedisQueue(newTestStorage(t).Client(), maxLen)
}

func rec(i int) *entity.GameRecord {
	return &entity.GameRecord{
		ID:          fmt.Sprintf("%032d", i),
		UserID:      "wx_u",
		Data:        []byte(fmt.Sprintf(`{"n":%d}`, i)),
		CreatedAtMs: int64(1000 + i),
	}
}

func TestGameRecordQueueFIFOAndTrim(t *testing.T) {
	q := newTestRecordQueue(t, 100)
	ctx := context.Background()
	for i := 1; i <= 5; i++ {
		if err := q.Append(ctx, rec(i)); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}

	if ok, err := q.AcquireFlushLock(ctx, "tok-1", time.Minute); err != nil || !ok {
		t.Fatalf("AcquireFlushLock = (%v, %v)", ok, err)
	}
	records, count, malformed, err := q.Peek(ctx, 3)
	if err != nil || count != 3 || malformed != 0 || len(records) != 3 {
		t.Fatalf("Peek = (%d records, count %d, malformed %d, %v)", len(records), count, malformed, err)
	}
	if records[0].ID != rec(1).ID || string(records[2].Data) != `{"n":3}` || records[0].CreatedAtMs != 1001 || records[0].UserID != "wx_u" {
		t.Errorf("peeked = %+v", records[0])
	}
	if ok, err := q.TrimIfLocked(ctx, "tok-1", count); err != nil || !ok {
		t.Fatalf("TrimIfLocked = (%v, %v)", ok, err)
	}
	if n, _ := q.Len(ctx); n != 2 {
		t.Errorf("len after trim = %d, want 2", n)
	}
	records, _, _, _ = q.Peek(ctx, 10)
	if len(records) != 2 || records[0].ID != rec(4).ID {
		t.Errorf("remaining head = %+v", records)
	}
}

func TestGameRecordQueueCap(t *testing.T) {
	q := newTestRecordQueue(t, 2)
	ctx := context.Background()
	_ = q.Append(ctx, rec(1))
	_ = q.Append(ctx, rec(2))
	if err := q.Append(ctx, rec(3)); !errors.Is(err, repository.ErrQueueFull) {
		t.Errorf("third append err = %v, want ErrQueueFull", err)
	}
}

func TestGameRecordFlushLockRules(t *testing.T) {
	q := newTestRecordQueue(t, 100)
	ctx := context.Background()
	_ = q.Append(ctx, rec(1))

	if ok, _ := q.AcquireFlushLock(ctx, "owner", time.Minute); !ok {
		t.Fatal("first acquire should succeed")
	}
	if ok, _ := q.AcquireFlushLock(ctx, "other", time.Minute); ok {
		t.Error("second instance must not acquire a held lock")
	}
	// 非持锁者不能删除队头
	if ok, err := q.TrimIfLocked(ctx, "other", 1); err != nil || ok {
		t.Errorf("trim by non-owner = (%v, %v), want refused", ok, err)
	}
	if n, _ := q.Len(ctx); n != 1 {
		t.Errorf("queue len = %d, want 1", n)
	}
	// 非持锁者不能释放锁
	_ = q.ReleaseFlushLock(ctx, "other")
	if ok, _ := q.AcquireFlushLock(ctx, "other", time.Minute); ok {
		t.Error("lock released by non-owner")
	}
	if err := q.ReleaseFlushLock(ctx, "owner"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if ok, _ := q.AcquireFlushLock(ctx, "other", time.Minute); !ok {
		t.Error("lock should be free after owner release")
	}
}

func TestGameRecordPeekCountsMalformedItems(t *testing.T) {
	q := newTestRecordQueue(t, 100)
	ctx := context.Background()
	_ = q.Append(ctx, rec(1))
	if err := q.client.RPush(ctx, keyGameRecordQueue, "not json").Err(); err != nil {
		t.Fatalf("seed malformed: %v", err)
	}
	records, count, malformed, err := q.Peek(ctx, 10)
	if err != nil || count != 2 || malformed != 1 || len(records) != 1 {
		t.Errorf("Peek = (%d, %d, %d, %v), want 1 record, count 2, malformed 1", len(records), count, malformed, err)
	}
}
