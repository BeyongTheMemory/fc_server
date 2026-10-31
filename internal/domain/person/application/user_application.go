package application

import (
	"context"
	"errors"

	"fc_server/internal/domain/person/entity"
	"fc_server/internal/domain/person/entity/vo"
)

// 应用层错误分类;微信上游错误见 provider 包。
var (
	ErrUnauthenticated = errors.New("unauthenticated")         // → 401
	ErrInvalidProfile  = errors.New("invalid profile")         // → 400
	ErrTooManyAttempts = errors.New("too many login attempts") // → 429
	ErrUnavailable     = errors.New("dependency unavailable")  // → 503
	// ErrInvalidFriendData 解密出的好友数据不属于当前玩家(code 与 token 不是同一微信号)→ 400
	ErrInvalidFriendData = errors.New("friend data does not belong to current player")
)

type UserApplication interface {
	// LoginWithWechat 用 wx.login 的 code 登录:兑换微信身份、创建或刷新玩家、签发新会话。
	// clientKey 是限流维度(可信来源 IP),为空时不限流。
	LoginWithWechat(ctx context.Context, code, clientKey string) (*entity.LoginResult, error)
	// Authenticate 校验业务 token,返回其会话;无效、过期、不属于本 AppID 均为 ErrUnauthenticated。
	Authenticate(ctx context.Context, token string) (*entity.Session, error)
	// GetPlayer 读取已认证玩家;玩家记录缺失按 ErrUnauthenticated 处理,促使客户端重新登录。
	GetPlayer(ctx context.Context, userID string) (*entity.Person, error)
	// GetPlayers 批量读取玩家资料(如排行榜展示头像昵称);不存在的 ID 不出现在结果中。
	GetPlayers(ctx context.Context, userIDs []string) (map[string]*entity.Person, error)
	// UpdateProfile 更新当前玩家的展示资料。
	UpdateProfile(ctx context.Context, userID string, profile vo.Profile) (*entity.Person, error)
	// SyncWechatFriends 解密 wx.getRelationFriendList 的数据并替换当前玩家的好友列表,
	// code 必须是调用该接口之前新取的 wx.login code。返回保存的好友数。
	SyncWechatFriends(ctx context.Context, userID, code, encryptedData, iv string) (int, error)
	// GetFriendIDs 当前玩家的好友 user_id;尚未同步或已过期返回 nil(区别于空列表)。
	GetFriendIDs(ctx context.Context, userID string) ([]string, error)
	// Logout 删除该 token 对应的会话,不影响其他设备。
	Logout(ctx context.Context, token string) error
}
