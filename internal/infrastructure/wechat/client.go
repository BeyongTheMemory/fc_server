// Package wechat 实现小游戏 code2Session 身份兑换(specs/002 plan §5)。
// 请求 URL 携带 AppSecret 与 code,因此本包返回的错误从不包含原始 *url.Error 或 URL。
package wechat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"fc_server/internal/domain/person/provider"
)

const (
	defaultBaseURL   = "https://api.weixin.qq.com"
	code2SessionPath = "/sns/jscode2session"
	maxResponseBytes = 64 << 10
)

// 需要区别处理的 code2Session 错误码;其余(含 -1 系统繁忙、40125 密钥错误)均为上游失败。
const (
	errCodeInvalidCode = 40029
	errCodeCodeUsed    = 40163
	errCodeRiskBlocked = 40226
	errCodeRateLimited = 45011
)

type Client struct {
	appID     string
	appSecret string
	baseURL   string
	http      *http.Client
}

var _ provider.WechatIdentityProvider = (*Client)(nil)

func NewClient(appID, appSecret string, timeout time.Duration) *Client {
	return &Client{
		appID:     appID,
		appSecret: appSecret,
		baseURL:   defaultBaseURL,
		http: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

type code2SessionResponse struct {
	OpenID     string `json:"openid"`
	UnionID    string `json:"unionid"`
	SessionKey string `json:"session_key"`
	ErrCode    int    `json:"errcode"`
}

func (c *Client) Code2Session(ctx context.Context, code string) (*provider.WechatIdentity, error) {
	r, err := c.code2Session(ctx, code)
	if err != nil {
		return nil, err
	}
	return &provider.WechatIdentity{OpenID: r.OpenID, UnionID: r.UnionID}, nil
}

// code2Session 返回的 session_key 只能留在本包内使用。
func (c *Client) code2Session(ctx context.Context, code string) (*code2SessionResponse, error) {
	query := url.Values{
		"appid":      {c.appID},
		"secret":     {c.appSecret},
		"js_code":    {code},
		"grant_type": {"authorization_code"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+code2SessionPath+"?"+query.Encode(), nil)
	if err != nil {
		return nil, &provider.UpstreamError{Kind: provider.ErrUpstream, Detail: "build request"}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, transportError(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, &provider.UpstreamError{Kind: provider.ErrUpstream, Detail: fmt.Sprintf("http status %d", resp.StatusCode)}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, transportError(err)
	}
	if len(body) > maxResponseBytes {
		return nil, &provider.UpstreamError{Kind: provider.ErrUpstream, Detail: "response too large"}
	}

	var r code2SessionResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, &provider.UpstreamError{Kind: provider.ErrUpstream, Detail: "invalid json response"}
	}
	if r.ErrCode != 0 {
		return nil, &provider.UpstreamError{Kind: classifyErrCode(r.ErrCode), ErrCode: r.ErrCode}
	}
	if r.OpenID == "" {
		return nil, &provider.UpstreamError{Kind: provider.ErrUpstream, Detail: "missing openid"}
	}
	return &r, nil
}

func classifyErrCode(code int) error {
	switch code {
	case errCodeInvalidCode, errCodeCodeUsed:
		return provider.ErrInvalidCode
	case errCodeRiskBlocked:
		return provider.ErrRiskBlocked
	case errCodeRateLimited:
		return provider.ErrRateLimited
	default:
		return provider.ErrUpstream
	}
}

// transportError 把网络错误归类为超时或上游失败,丢弃可能携带 URL 的原始错误文本。
func transportError(err error) error {
	var netErr net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return &provider.UpstreamError{Kind: provider.ErrUpstreamTimeout}
	case errors.Is(err, context.Canceled):
		return &provider.UpstreamError{Kind: provider.ErrUpstreamTimeout, Detail: "request canceled"}
	default:
		return &provider.UpstreamError{Kind: provider.ErrUpstream, Detail: "transport error"}
	}
}
