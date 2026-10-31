package impl

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"fc_server/internal/domain/person/application"
	"fc_server/internal/domain/person/entity"
	"fc_server/internal/domain/person/entity/vo"
	"fc_server/internal/domain/person/provider"
	"fc_server/internal/domain/person/repository"
)

const (
	tokenBytes     = 32
	maxTokenLength = 256
	loginLimitKey  = "login:"
)

// Options 用户应用服务的运行参数。
type Options struct {
	AppID           string
	SessionTTL      time.Duration
	LoginRateLimit  int
	LoginRateWindow time.Duration
	FriendTTL       time.Duration // 好友列表有效期,过期后需重新同步
	MaxFriends      int           // 每个玩家最多保存的好友数
}

var (
	userApplicationImpl *UserApplicationImpl
	_                   application.UserApplication = (*UserApplicationImpl)(nil)
)

func Init(identity provider.WechatIdentityProvider, players repository.PlayerRepository,
	sessions repository.SessionRepository, limiter repository.AttemptLimiter,
	friends repository.FriendRepository, opts Options) {
	userApplicationImpl = New(identity, players, sessions, limiter, friends, opts)
}

func GetUserApplicationImpl() application.UserApplication {
	if userApplicationImpl == nil {
		panic("userApplicationImpl is not initialized")
	}
	return userApplicationImpl
}

type UserApplicationImpl struct {
	identity provider.WechatIdentityProvider
	players  repository.PlayerRepository
	sessions repository.SessionRepository
	limiter  repository.AttemptLimiter
	friends  repository.FriendRepository
	opts     Options
	now      func() time.Time
}

func New(identity provider.WechatIdentityProvider, players repository.PlayerRepository,
	sessions repository.SessionRepository, limiter repository.AttemptLimiter,
	friends repository.FriendRepository, opts Options) *UserApplicationImpl {
	return &UserApplicationImpl{
		identity: identity,
		players:  players,
		sessions: sessions,
		limiter:  limiter,
		friends:  friends,
		opts:     opts,
		now:      time.Now,
	}
}

func (a *UserApplicationImpl) LoginWithWechat(ctx context.Context, code, clientKey string) (*entity.LoginResult, error) {
	if clientKey != "" {
		allowed, err := a.limiter.Allow(ctx, loginLimitKey+clientKey, a.opts.LoginRateLimit, a.opts.LoginRateWindow)
		if err != nil {
			return nil, fmt.Errorf("%w: login limiter: %w", application.ErrUnavailable, err)
		}
		if !allowed {
			return nil, application.ErrTooManyAttempts
		}
	}

	identity, err := a.identity.Code2Session(ctx, code)
	if err != nil {
		return nil, err
	}
	if identity == nil || identity.OpenID == "" {
		return nil, &provider.UpstreamError{Kind: provider.ErrUpstream, Detail: "empty openid"}
	}

	now := a.now()
	userID := entity.PlayerID(a.opts.AppID, identity.OpenID)
	player, err := a.players.UpsertOnLogin(ctx, userID, a.opts.AppID, identity.OpenID, now.Unix())
	if err != nil {
		if errors.Is(err, repository.ErrIdentityMismatch) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: upsert player: %w", application.ErrUnavailable, err)
	}

	token, err := newToken()
	if err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}
	session := &entity.Session{
		UserID:    userID,
		AppID:     a.opts.AppID,
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(a.opts.SessionTTL).Unix(),
	}
	if err := a.sessions.Create(ctx, entity.HashToken(token), session, a.opts.SessionTTL); err != nil {
		return nil, fmt.Errorf("%w: create session: %w", application.ErrUnavailable, err)
	}
	return &entity.LoginResult{Token: token, ExpiresAt: session.ExpiresAt, Player: player}, nil
}

func (a *UserApplicationImpl) Authenticate(ctx context.Context, token string) (*entity.Session, error) {
	if token == "" || len(token) > maxTokenLength {
		return nil, application.ErrUnauthenticated
	}
	session, err := a.sessions.Get(ctx, entity.HashToken(token))
	if errors.Is(err, repository.ErrSessionNotFound) {
		return nil, application.ErrUnauthenticated
	}
	if err != nil {
		return nil, fmt.Errorf("%w: get session: %w", application.ErrUnavailable, err)
	}
	if session.AppID != a.opts.AppID || session.UserID == "" || session.ExpiresAt <= a.now().Unix() {
		return nil, application.ErrUnauthenticated
	}
	return session, nil
}

func (a *UserApplicationImpl) GetPlayer(ctx context.Context, userID string) (*entity.Person, error) {
	player, err := a.players.Get(ctx, userID)
	if errors.Is(err, repository.ErrPlayerNotFound) {
		return nil, application.ErrUnauthenticated
	}
	if err != nil {
		return nil, fmt.Errorf("%w: get player: %w", application.ErrUnavailable, err)
	}
	return player, nil
}

func (a *UserApplicationImpl) GetPlayers(ctx context.Context, userIDs []string) (map[string]*entity.Person, error) {
	players, err := a.players.GetMany(ctx, userIDs)
	if err != nil {
		return nil, fmt.Errorf("%w: get players: %w", application.ErrUnavailable, err)
	}
	return players, nil
}

func (a *UserApplicationImpl) UpdateProfile(ctx context.Context, userID string, profile vo.Profile) (*entity.Person, error) {
	if err := profile.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %s", application.ErrInvalidProfile, err.Error())
	}
	player, err := a.players.UpdateProfile(ctx, userID, profile, a.now().Unix())
	if errors.Is(err, repository.ErrPlayerNotFound) {
		return nil, application.ErrUnauthenticated
	}
	if err != nil {
		return nil, fmt.Errorf("%w: update profile: %w", application.ErrUnavailable, err)
	}
	return player, nil
}

func (a *UserApplicationImpl) SyncWechatFriends(ctx context.Context, userID, code, encryptedData, iv string) (int, error) {
	player, err := a.GetPlayer(ctx, userID)
	if err != nil {
		return 0, err
	}
	data, err := a.identity.RelationFriends(ctx, code, encryptedData, iv)
	if err != nil {
		return 0, err
	}
	if data.OpenID != player.OpenID {
		return 0, application.ErrInvalidFriendData
	}
	seen := map[string]struct{}{userID: {}}
	friendIDs := make([]string, 0, len(data.FriendOpenIDs))
	for _, openID := range data.FriendOpenIDs {
		id := entity.PlayerID(a.opts.AppID, openID)
		if _, dup := seen[id]; dup || openID == "" {
			continue
		}
		seen[id] = struct{}{}
		friendIDs = append(friendIDs, id)
		if len(friendIDs) >= a.opts.MaxFriends {
			break
		}
	}
	if err := a.friends.Replace(ctx, userID, friendIDs, a.opts.FriendTTL); err != nil {
		return 0, fmt.Errorf("%w: save friends: %w", application.ErrUnavailable, err)
	}
	return len(friendIDs), nil
}

func (a *UserApplicationImpl) GetFriendIDs(ctx context.Context, userID string) ([]string, error) {
	ids, synced, err := a.friends.List(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("%w: list friends: %w", application.ErrUnavailable, err)
	}
	if !synced {
		return nil, nil
	}
	if ids == nil {
		ids = []string{}
	}
	return ids, nil
}

func (a *UserApplicationImpl) Logout(ctx context.Context, token string) error {
	if err := a.sessions.Delete(ctx, entity.HashToken(token)); err != nil {
		return fmt.Errorf("%w: delete session: %w", application.ErrUnavailable, err)
	}
	return nil
}

func newToken() (string, error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
