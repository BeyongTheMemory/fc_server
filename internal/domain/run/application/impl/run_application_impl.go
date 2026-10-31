package impl

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"time"

	"fc_server/internal/domain/run/application"
	"fc_server/internal/domain/run/entity"
	"fc_server/internal/domain/run/repository"
)

const (
	// 服务端种子与前端默认种子同格式:8 位字母数字
	seedAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	seedLength   = 8
	startKey     = "run_start:"
)

// Options 对局票规则。
type Options struct {
	TicketTTL     time.Duration // 对局票有效期(覆盖存档后隔天续玩)
	StartLimit    int           // 每个玩家在 StartWindow 内最多开局次数
	StartWindow   time.Duration
	MinMsPerStage int64 // 每关最短耗时,0 表示不校验
}

var (
	runApplicationImpl *RunApplicationImpl
	_                  application.RunApplication = (*RunApplicationImpl)(nil)
)

func Init(repo repository.RunRepository, limiter repository.StartLimiter, opts Options) {
	runApplicationImpl = New(repo, limiter, opts)
}

func GetRunApplicationImpl() application.RunApplication {
	if runApplicationImpl == nil {
		panic("runApplicationImpl is not initialized")
	}
	return runApplicationImpl
}

type RunApplicationImpl struct {
	repo    repository.RunRepository
	limiter repository.StartLimiter
	opts    Options
	now     func() time.Time
}

func New(repo repository.RunRepository, limiter repository.StartLimiter, opts Options) *RunApplicationImpl {
	return &RunApplicationImpl{repo: repo, limiter: limiter, opts: opts, now: time.Now}
}

func (a *RunApplicationImpl) Start(ctx context.Context, userID string) (*entity.Run, error) {
	allowed, err := a.limiter.Allow(ctx, startKey+userID, a.opts.StartLimit, a.opts.StartWindow)
	if err != nil {
		return nil, fmt.Errorf("%w: start limiter: %w", application.ErrUnavailable, err)
	}
	if !allowed {
		return nil, application.ErrTooManyRuns
	}

	seed, err := randomSeed()
	if err != nil {
		return nil, fmt.Errorf("generate seed: %w", err)
	}
	id, err := randomID()
	if err != nil {
		return nil, fmt.Errorf("generate run id: %w", err)
	}
	now := a.now()
	run := &entity.Run{
		ID:          id,
		UserID:      userID,
		Seed:        seed,
		StartedAtMs: now.UnixMilli(),
		ExpiresAtMs: now.Add(a.opts.TicketTTL).UnixMilli(),
	}
	if err := a.repo.Create(ctx, run, a.opts.TicketTTL); err != nil {
		return nil, fmt.Errorf("%w: create run: %w", application.ErrUnavailable, err)
	}
	return run, nil
}

func (a *RunApplicationImpl) AuthorizeScore(ctx context.Context, userID, runID string, stage int64, fingerprint string) error {
	run, err := a.repo.Get(ctx, runID)
	if errors.Is(err, repository.ErrRunNotFound) {
		return application.ErrRunNotFound
	}
	if err != nil {
		return fmt.Errorf("%w: get run: %w", application.ErrUnavailable, err)
	}
	// 别人的对局票与不存在的票不作区分,避免探测
	if run.UserID != userID {
		return application.ErrRunNotFound
	}
	nowMs := a.now().UnixMilli()
	if a.opts.MinMsPerStage > 0 && nowMs-run.StartedAtMs < stage*a.opts.MinMsPerStage {
		return application.ErrRunTooFast
	}
	result, err := a.repo.ClaimSubmission(ctx, runID, fingerprint, nowMs)
	if errors.Is(err, repository.ErrRunNotFound) {
		return application.ErrRunNotFound
	}
	if err != nil {
		return fmt.Errorf("%w: claim run: %w", application.ErrUnavailable, err)
	}
	if result == repository.ClaimConflict {
		return application.ErrRunConflict
	}
	return nil
}

func randomID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func randomSeed() (string, error) {
	out := make([]byte, seedLength)
	max := big.NewInt(int64(len(seedAlphabet)))
	for i := range out {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = seedAlphabet[n.Int64()]
	}
	return string(out), nil
}
