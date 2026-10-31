package repository_impl

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"fc_server/internal/domain/run/entity"
	"fc_server/internal/domain/run/repository"
)

func TestRunStorageRoundTripAndTTL(t *testing.T) {
	client := newTestStorage(t).Client()
	s := NewRunRedisStorage(client)
	ctx := context.Background()
	run := &entity.Run{ID: "r1", UserID: "wx_u", Seed: "Ab3dE9xQ", StartedAtMs: 1000, ExpiresAtMs: 2000}

	if err := s.Create(ctx, run, time.Hour); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := s.Get(ctx, "r1")
	if err != nil || *got != *run {
		t.Fatalf("Get = (%+v, %v), want %+v", got, err, run)
	}
	if ttl := client.PTTL(ctx, runKey("r1")).Val(); ttl <= 0 || ttl > time.Hour {
		t.Errorf("TTL = %v", ttl)
	}
	if _, err := s.Get(ctx, "missing"); !errors.Is(err, repository.ErrRunNotFound) {
		t.Errorf("missing run err = %v", err)
	}
}

func TestRunClaimSubmission(t *testing.T) {
	s := NewRunRedisStorage(newTestStorage(t).Client())
	ctx := context.Background()
	if err := s.Create(ctx, &entity.Run{ID: "r1", UserID: "wx_u"}, time.Hour); err != nil {
		t.Fatalf("Create: %v", err)
	}
	steps := []struct {
		fp   string
		want repository.ClaimResult
	}{
		{"fp-a", repository.ClaimNew},
		{"fp-a", repository.ClaimReplay},
		{"fp-b", repository.ClaimConflict},
		{"fp-a", repository.ClaimReplay},
	}
	for i, st := range steps {
		got, err := s.ClaimSubmission(ctx, "r1", st.fp, 5000)
		if err != nil || got != st.want {
			t.Errorf("step %d (%s) = (%v, %v), want %v", i, st.fp, got, err, st.want)
		}
	}
	if _, err := s.ClaimSubmission(ctx, "missing", "fp", 1); !errors.Is(err, repository.ErrRunNotFound) {
		t.Errorf("missing run err = %v", err)
	}
}

func TestRunConcurrentClaimsOnlyOneWins(t *testing.T) {
	s := NewRunRedisStorage(newTestStorage(t).Client())
	ctx := context.Background()
	_ = s.Create(ctx, &entity.Run{ID: "r1", UserID: "wx_u"}, time.Hour)

	var mu sync.Mutex
	counts := map[repository.ClaimResult]int{}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			fp := "fp-a"
			if i%2 == 1 {
				fp = "fp-b"
			}
			res, err := s.ClaimSubmission(ctx, "r1", fp, int64(i))
			if err != nil {
				t.Errorf("claim: %v", err)
				return
			}
			mu.Lock()
			counts[res]++
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	if counts[repository.ClaimNew] != 1 || counts[repository.ClaimReplay] != 9 || counts[repository.ClaimConflict] != 10 {
		t.Errorf("claims = %v, want exactly one new, 9 replays, 10 conflicts", counts)
	}
}
