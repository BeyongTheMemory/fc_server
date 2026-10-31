// Package runtest 提供 run 领域接口的内存实现,供各层测试使用。
package runtest

import (
	"context"
	"sync"
	"time"

	"fc_server/internal/domain/run/entity"
	"fc_server/internal/domain/run/repository"
)

// Runs 内存对局票存储(不模拟过期)。
type Runs struct {
	mu        sync.Mutex
	Data      map[string]*entity.Run
	Submitted map[string]string
	Err       error
}

func NewRuns() *Runs {
	return &Runs{Data: map[string]*entity.Run{}, Submitted: map[string]string{}}
}

func (r *Runs) Create(_ context.Context, run *entity.Run, _ time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return r.Err
	}
	clone := *run
	r.Data[run.ID] = &clone
	return nil
}

func (r *Runs) Get(_ context.Context, runID string) (*entity.Run, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return nil, r.Err
	}
	run, ok := r.Data[runID]
	if !ok {
		return nil, repository.ErrRunNotFound
	}
	clone := *run
	return &clone, nil
}

func (r *Runs) ClaimSubmission(_ context.Context, runID, fingerprint string, _ int64) (repository.ClaimResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return 0, r.Err
	}
	if _, ok := r.Data[runID]; !ok {
		return 0, repository.ErrRunNotFound
	}
	cur, done := r.Submitted[runID]
	switch {
	case !done:
		r.Submitted[runID] = fingerprint
		return repository.ClaimNew, nil
	case cur == fingerprint:
		return repository.ClaimReplay, nil
	default:
		return repository.ClaimConflict, nil
	}
}

// Limiter 内存计数限流,不按时间滚动窗口。
type Limiter struct {
	mu     sync.Mutex
	Counts map[string]int
	Err    error
}

func NewLimiter() *Limiter {
	return &Limiter{Counts: map[string]int{}}
}

func (l *Limiter) Allow(_ context.Context, key string, limit int, _ time.Duration) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.Err != nil {
		return false, l.Err
	}
	l.Counts[key]++
	return l.Counts[key] <= limit, nil
}
