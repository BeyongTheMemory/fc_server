package processor

import (
	"context"
	"fmt"

	personimpl "fc_server/internal/domain/person/application/impl"
	"fc_server/internal/domain/person/entity"
	"fc_server/internal/domain/rank/application/impl"
	"fc_server/internal/domain/rank/entity/vo"
	runimpl "fc_server/internal/domain/run/application/impl"
	"fc_server/internal/processor/dto"
)

// UploadScore 为已认证玩家凭对局票上报一局成绩。请求带 profile 时先保存头像昵称
// (不合法则整体失败、不记分),否则沿用已保存的资料;榜单身份与昵称都来自服务端。
func UploadScore(ctx context.Context, userID, clientIP string, request *dto.UploadScoreRequest) (*dto.UploadScoreResponse, error) {
	if err := request.Validate(maxStage); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidParam, err.Error())
	}
	score := request.ToScore()
	if err := runimpl.GetRunApplicationImpl().AuthorizeScore(ctx, userID, request.RunId, score.Stage, score.Fingerprint()); err != nil {
		return nil, err
	}
	users := personimpl.GetUserApplicationImpl()
	var player *entity.Person
	var err error
	if request.Profile != nil {
		player, err = users.UpdateProfile(ctx, userID, request.Profile.ToProfile())
	} else {
		player, err = users.GetPlayer(ctx, userID)
	}
	if err != nil {
		return nil, err
	}
	user := &vo.UserInfo{UserId: player.ID, UserName: player.DisplayName()}
	updated, best, err := impl.GetRankApplicationImpl().UploadScore(ctx, request.Location.ToLocation(), user, score, clientIP)
	if err != nil {
		return nil, err
	}
	return &dto.UploadScoreResponse{Updated: updated, Best: dto.NewScoreDto(best)}, nil
}
