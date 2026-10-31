package processor

import (
	"context"
	"fmt"

	"fc_server/internal/domain/person/application/impl"
	"fc_server/internal/processor/dto"
)

// WechatLogin 用 wx.login 的 code 登录;clientIP 为可信来源 IP,用于登录限流。
func WechatLogin(ctx context.Context, req *dto.WechatLoginRequest, clientIP string) (*dto.WechatLoginResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidParam, err.Error())
	}
	res, err := impl.GetUserApplicationImpl().LoginWithWechat(ctx, req.Code, clientIP)
	if err != nil {
		return nil, err
	}
	return &dto.WechatLoginResponse{
		Token:     res.Token,
		ExpiresAt: res.ExpiresAt,
		User:      dto.NewUserDto(res.Player),
	}, nil
}

func GetCurrentUser(ctx context.Context, userID string) (*dto.UserDto, error) {
	player, err := impl.GetUserApplicationImpl().GetPlayer(ctx, userID)
	if err != nil {
		return nil, err
	}
	return dto.NewUserDto(player), nil
}

func UpdateProfile(ctx context.Context, userID string, req *dto.ProfileDto) (*dto.UserDto, error) {
	if req == nil {
		return nil, fmt.Errorf("%w: request body is required", ErrInvalidParam)
	}
	player, err := impl.GetUserApplicationImpl().UpdateProfile(ctx, userID, req.ToProfile())
	if err != nil {
		return nil, err
	}
	return dto.NewUserDto(player), nil
}

// SyncFriends 解密 wx.getRelationFriendList 的数据,保存为当前玩家的好友列表。
func SyncFriends(ctx context.Context, userID string, req *dto.FriendSyncRequest) (*dto.FriendSyncResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidParam, err.Error())
	}
	n, err := impl.GetUserApplicationImpl().SyncWechatFriends(ctx, userID, req.Code, req.EncryptedData, req.Iv)
	if err != nil {
		return nil, err
	}
	return &dto.FriendSyncResponse{FriendCount: n}, nil
}

func Logout(ctx context.Context, token string) error {
	return impl.GetUserApplicationImpl().Logout(ctx, token)
}
