package impl

import (
	"context"
	"fmt"
	"time"

	"fc_server/internal/domain/achievement/application"
	"fc_server/internal/domain/achievement/entity"
	"fc_server/internal/domain/achievement/repository"
)

var (
	achievementApplicationImpl *AchievementApplicationImpl
	_                          application.AchievementApplication = (*AchievementApplicationImpl)(nil)
)

func Init(repo repository.AchievementRepository) {
	achievementApplicationImpl = New(repo)
}

func GetAchievementApplicationImpl() application.AchievementApplication {
	if achievementApplicationImpl == nil {
		panic("achievementApplicationImpl is not initialized")
	}
	return achievementApplicationImpl
}

type AchievementApplicationImpl struct {
	repo repository.AchievementRepository
	now  func() time.Time
}

func New(repo repository.AchievementRepository) *AchievementApplicationImpl {
	return &AchievementApplicationImpl{repo: repo, now: time.Now}
}

func (a *AchievementApplicationImpl) Unlock(ctx context.Context, userID, achievementID string, unlockedAtMs int64, detail []byte, clientIP string) (*entity.Achievement, bool, error) {
	now := a.now().UnixMilli()
	if unlockedAtMs == 0 {
		unlockedAtMs = now
	}
	saved, created, err := a.repo.Unlock(ctx, &entity.Achievement{
		UserID:        userID,
		AchievementID: achievementID,
		UnlockedAtMs:  unlockedAtMs,
		ReceivedAtMs:  now,
		Detail:        detail,
		ClientIP:      clientIP,
	})
	if err != nil {
		return nil, false, fmt.Errorf("%w: %w", application.ErrUnavailable, err)
	}
	return saved, created, nil
}
