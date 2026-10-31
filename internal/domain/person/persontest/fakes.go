// Package persontest 提供 person 领域接口的内存实现,供各层测试隔离微信与 Redis。
package persontest

import (
	"context"
	"sync"
	"time"

	"fc_server/internal/domain/person/entity"
	"fc_server/internal/domain/person/entity/vo"
	"fc_server/internal/domain/person/provider"
	"fc_server/internal/domain/person/repository"
)

// FakeWechat 按 code 返回预置 openid;未登记的 code 视为无效。
// Friends 为 RelationFriends 返回的好友 openid(按 code 对应的 openid 查找)。
type FakeWechat struct {
	mu        sync.Mutex
	OpenIDs   map[string]string
	Friends   map[string][]string
	Err       error
	FriendErr error
	Calls     int
}

func NewFakeWechat() *FakeWechat {
	return &FakeWechat{OpenIDs: map[string]string{}, Friends: map[string][]string{}}
}

func (f *FakeWechat) RelationFriends(_ context.Context, code, _, _ string) (*provider.RelationFriends, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.FriendErr != nil {
		return nil, f.FriendErr
	}
	openID, ok := f.OpenIDs[code]
	if !ok {
		return nil, &provider.UpstreamError{Kind: provider.ErrInvalidCode, ErrCode: 40029}
	}
	return &provider.RelationFriends{OpenID: openID, FriendOpenIDs: f.Friends[openID]}, nil
}

func (f *FakeWechat) Code2Session(_ context.Context, code string) (*provider.WechatIdentity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls++
	if f.Err != nil {
		return nil, f.Err
	}
	openID, ok := f.OpenIDs[code]
	if !ok {
		return nil, &provider.UpstreamError{Kind: provider.ErrInvalidCode, ErrCode: 40029}
	}
	return &provider.WechatIdentity{OpenID: openID}, nil
}

// Players 内存玩家存储,语义与 Redis 实现一致(资料不变不递增 revision)。
type Players struct {
	mu      sync.Mutex
	Data    map[string]*entity.Person
	Err     error
	Updates int
}

func NewPlayers() *Players {
	return &Players{Data: map[string]*entity.Person{}}
}

func (p *Players) UpsertOnLogin(_ context.Context, userID, appID, openID string, now int64) (*entity.Person, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Err != nil {
		return nil, p.Err
	}
	cur, ok := p.Data[userID]
	if !ok {
		cur = &entity.Person{ID: userID, AppID: appID, OpenID: openID, CreatedTime: now, UpdatedTime: now}
		p.Data[userID] = cur
	} else if cur.AppID != appID || cur.OpenID != openID {
		return nil, repository.ErrIdentityMismatch
	}
	cur.LastLoginTime = now
	cur.Revision++
	clone := *cur
	return &clone, nil
}

func (p *Players) Get(_ context.Context, userID string) (*entity.Person, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Err != nil {
		return nil, p.Err
	}
	cur, ok := p.Data[userID]
	if !ok {
		return nil, repository.ErrPlayerNotFound
	}
	clone := *cur
	return &clone, nil
}

func (p *Players) GetMany(_ context.Context, userIDs []string) (map[string]*entity.Person, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Err != nil {
		return nil, p.Err
	}
	out := make(map[string]*entity.Person, len(userIDs))
	for _, id := range userIDs {
		if cur, ok := p.Data[id]; ok {
			clone := *cur
			out[id] = &clone
		}
	}
	return out, nil
}

func (p *Players) UpdateProfile(_ context.Context, userID string, profile vo.Profile, now int64) (*entity.Person, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Err != nil {
		return nil, p.Err
	}
	cur, ok := p.Data[userID]
	if !ok {
		return nil, repository.ErrPlayerNotFound
	}
	if cur.Profile != profile {
		cur.Profile = profile
		cur.UpdatedTime = now
		cur.Revision++
		p.Updates++
	}
	clone := *cur
	return &clone, nil
}

// Sessions 内存会话存储;过期由调用方传入的 ttl 与 Now 判定。
type Sessions struct {
	mu   sync.Mutex
	Data map[string]sessionEntry
	Err  error
	Now  func() time.Time
}

type sessionEntry struct {
	session  entity.Session
	deadline time.Time
}

func NewSessions() *Sessions {
	return &Sessions{Data: map[string]sessionEntry{}, Now: time.Now}
}

func (s *Sessions) Create(_ context.Context, tokenHash string, session *entity.Session, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return s.Err
	}
	s.Data[tokenHash] = sessionEntry{session: *session, deadline: s.Now().Add(ttl)}
	return nil
}

func (s *Sessions) Get(_ context.Context, tokenHash string) (*entity.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return nil, s.Err
	}
	e, ok := s.Data[tokenHash]
	if !ok || !s.Now().Before(e.deadline) {
		return nil, repository.ErrSessionNotFound
	}
	clone := e.session
	return &clone, nil
}

func (s *Sessions) Delete(_ context.Context, tokenHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return s.Err
	}
	delete(s.Data, tokenHash)
	return nil
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

// Friends 内存好友存储(不模拟过期)。
type Friends struct {
	mu   sync.Mutex
	Data map[string][]string
	Err  error
}

func NewFriends() *Friends {
	return &Friends{Data: map[string][]string{}}
}

func (f *Friends) Replace(_ context.Context, userID string, friendIDs []string, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return f.Err
	}
	f.Data[userID] = append([]string{}, friendIDs...)
	return nil
}

func (f *Friends) List(_ context.Context, userID string) ([]string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return nil, false, f.Err
	}
	ids, ok := f.Data[userID]
	return append([]string{}, ids...), ok, nil
}
