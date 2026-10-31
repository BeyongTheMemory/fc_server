package provider

import (
	"context"
	"errors"
	"fmt"
)

// 微信身份兑换的错误分类,HTTP 层据此映射状态码(specs/002 plan §5 错误行为)。
var (
	ErrInvalidCode     = errors.New("wechat login code invalid or used")    // 40029/40163 → 401
	ErrRiskBlocked     = errors.New("wechat login blocked by risk control") // 40226 → 403
	ErrRateLimited     = errors.New("wechat api rate limited")              // 45011 → 429
	ErrUpstreamTimeout = errors.New("wechat api timeout")                   // → 504
	ErrUpstream        = errors.New("wechat api failure")                   // → 502
	// ErrInvalidEncryptedData 开放数据解密失败或不属于本小游戏(session_key 不匹配、数据被篡改)→ 400
	ErrInvalidEncryptedData = errors.New("wechat encrypted data invalid")
)

// UpstreamError 携带微信 errcode 的分类错误。Error() 只含操作名与 errcode,
// 不含请求 URL、code 或密钥,可以安全写日志。
type UpstreamError struct {
	Kind    error
	ErrCode int
	Detail  string
}

func (e *UpstreamError) Error() string {
	msg := fmt.Sprintf("code2session: %v", e.Kind)
	if e.ErrCode != 0 {
		msg += fmt.Sprintf(" (errcode=%d)", e.ErrCode)
	}
	if e.Detail != "" {
		msg += ": " + e.Detail
	}
	return msg
}

func (e *UpstreamError) Unwrap() error { return e.Kind }

// WechatIdentity code2Session 成功后对业务有用的身份信息;session_key 不在其中,
// 本服务不解密微信资料,也不持久化或下发 session_key。
type WechatIdentity struct {
	OpenID  string
	UnionID string // 可选,首次登录不要求
}

// RelationFriends wx.getRelationFriendList 解密结果:数据所属玩家与其同玩互动好友的 openid。
type RelationFriends struct {
	OpenID        string
	FriendOpenIDs []string
}

// WechatIdentityProvider 用 wx.login 的临时 code 兑换微信身份。
// AppID/AppSecret 由实现方从服务端配置读取,调用方无法指定。
type WechatIdentityProvider interface {
	Code2Session(ctx context.Context, code string) (*WechatIdentity, error)
	// RelationFriends 用新 code 换取 session_key,解密 wx.getRelationFriendList 返回的
	// encryptedData/iv。session_key 只在实现内部使用,不返回、不存储。
	RelationFriends(ctx context.Context, code, encryptedData, iv string) (*RelationFriends, error)
}
