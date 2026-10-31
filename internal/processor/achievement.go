package processor

import (
	"context"
	"fmt"
	"time"

	"fc_server/internal/domain/achievement/application/impl"
	"fc_server/internal/processor/dto"
)

// UploadAchievement 为已认证玩家记录一项成就的首次解锁;重复上传返回首次记录。
func UploadAchievement(ctx context.Context, userID, clientIP string, req *dto.UploadAchievementRequest) (*dto.UploadAchievementResponse, error) {
	if err := req.Validate(time.Now()); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidParam, err.Error())
	}
	a, created, err := impl.GetAchievementApplicationImpl().Unlock(ctx, userID, req.AchievementId, req.UnlockedAtMs, req.NormalizedDetail(), clientIP)
	if err != nil {
		return nil, err
	}
	return &dto.UploadAchievementResponse{AchievementId: a.AchievementID, NewlyUnlocked: created, UnlockedAtMs: a.UnlockedAtMs}, nil
}
