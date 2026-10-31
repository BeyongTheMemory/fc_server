package repository_impl

import (
	"context"
	"errors"
	"sync"
	"testing"

	"fc_server/internal/domain/achievement/entity"
	"fc_server/internal/domain/achievement/repository"
)

func TestAchievementUnlockOnceAndQueue(t *testing.T) {
	s := NewAchievementRedisStorage(newTestStorage(t).Client(), 100)
	ctx := context.Background()
	first := &entity.Achievement{UserID: "wx_u", AchievementID: "first_ascension", UnlockedAtMs: 1000, ReceivedAtMs: 1100, Detail: []byte(`{"stage":19}`)}

	got, created, err := s.Unlock(ctx, first)
	if err != nil || !created || got.UnlockedAtMs != 1000 || string(got.Detail) != `{"stage":19}` {
		t.Fatalf("first unlock = (%+v, %v, %v)", got, created, err)
	}
	// 重复解锁:返回首次记录,不再入队
	got, created, err = s.Unlock(ctx, &entity.Achievement{UserID: "wx_u", AchievementID: "first_ascension", UnlockedAtMs: 5000, ReceivedAtMs: 5100})
	if err != nil || created || got.UnlockedAtMs != 1000 || got.ReceivedAtMs != 1100 || string(got.Detail) != `{"stage":19}` {
		t.Errorf("repeat unlock = (%+v, %v, %v)", got, created, err)
	}
	// 其他玩家的同一成就互不影响;无 detail 也可以
	if _, created, _ := s.Unlock(ctx, &entity.Achievement{UserID: "wx_v", AchievementID: "first_ascension", UnlockedAtMs: 1, ReceivedAtMs: 2}); !created {
		t.Error("another player's unlock must be independent")
	}

	items, count, malformed, err := s.Peek(ctx, 10)
	if err != nil || count != 2 || malformed != 0 || len(items) != 2 {
		t.Fatalf("Peek = (%d items, %d, %d, %v), want 2 queued", len(items), count, malformed, err)
	}
	if items[0].UserID != "wx_u" || items[0].AchievementID != "first_ascension" || items[0].UnlockedAtMs != 1000 || string(items[0].Detail) != `{"stage":19}` {
		t.Errorf("queued[0] = %+v", items[0])
	}
	if items[1].Detail != nil {
		t.Errorf("queued[1] detail = %s, want nil", items[1].Detail)
	}
}

func TestAchievementConcurrentUnlockQueuesOnce(t *testing.T) {
	s := NewAchievementRedisStorage(newTestStorage(t).Client(), 100)
	ctx := context.Background()
	var wg sync.WaitGroup
	createdCount := 0
	var mu sync.Mutex
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, created, err := s.Unlock(ctx, &entity.Achievement{UserID: "wx_u", AchievementID: "a", UnlockedAtMs: int64(i), ReceivedAtMs: int64(i)})
			if err != nil {
				t.Errorf("unlock: %v", err)
			}
			if created {
				mu.Lock()
				createdCount++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if n, _ := s.Len(ctx); createdCount != 1 || n != 1 {
		t.Errorf("created %d times, queue len %d; want exactly once", createdCount, n)
	}
}

func TestAchievementQueueCapLeavesNothingHalfSaved(t *testing.T) {
	s := NewAchievementRedisStorage(newTestStorage(t).Client(), 1)
	ctx := context.Background()
	if _, _, err := s.Unlock(ctx, &entity.Achievement{UserID: "wx_u", AchievementID: "a"}); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, _, err := s.Unlock(ctx, &entity.Achievement{UserID: "wx_u", AchievementID: "b"}); !errors.Is(err, repository.ErrQueueFull) {
		t.Fatalf("err = %v, want ErrQueueFull", err)
	}
	// 被拒绝的成就不能被记为已解锁,否则之后永远不会持久化
	if s.client.HExists(ctx, achievementsKey("wx_u"), "b").Val() {
		t.Error("rejected unlock must not be saved")
	}
}
