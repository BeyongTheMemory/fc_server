package wechat

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"fc_server/internal/domain/person/provider"
)

const (
	testAppID  = "wx_app_id"
	testSecret = "super-secret-value"
	testCode   = "one-time-login-code"
)

func newTestClient(t *testing.T, handler http.HandlerFunc, timeout time.Duration) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := NewClient(testAppID, testSecret, timeout)
	c.baseURL = srv.URL
	return c
}

func assertNoSecrets(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	for _, s := range []string{testSecret, testCode, "secret=", "js_code", "http://127.0.0.1"} {
		if strings.Contains(err.Error(), s) {
			t.Errorf("error text leaks %q: %v", s, err)
		}
	}
}

func TestCode2SessionSuccessSendsConfiguredCredentials(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != code2SessionPath || q.Get("appid") != testAppID || q.Get("secret") != testSecret ||
			q.Get("js_code") != testCode || q.Get("grant_type") != "authorization_code" {
			t.Errorf("unexpected request: %s", r.URL.String())
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(`{"openid":"o-123","session_key":"sk","unionid":"u-9"}`))
	}, time.Second)

	id, err := c.Code2Session(context.Background(), testCode)
	if err != nil {
		t.Fatalf("Code2Session: %v", err)
	}
	if id.OpenID != "o-123" || id.UnionID != "u-9" {
		t.Errorf("identity = %+v", id)
	}
}

func TestCode2SessionWithoutUnionIDSucceeds(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"openid":"o-123","session_key":"sk"}`))
	}, time.Second)
	id, err := c.Code2Session(context.Background(), testCode)
	if err != nil || id.OpenID != "o-123" || id.UnionID != "" {
		t.Fatalf("got (%+v, %v)", id, err)
	}
}

func TestCode2SessionErrorClassification(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		want    error
		errCode int
	}{
		{"invalid code", 200, `{"errcode":40029,"errmsg":"invalid code"}`, provider.ErrInvalidCode, 40029},
		{"code used", 200, `{"errcode":40163,"errmsg":"code been used"}`, provider.ErrInvalidCode, 40163},
		{"risk", 200, `{"errcode":40226,"errmsg":"high risk"}`, provider.ErrRiskBlocked, 40226},
		{"rate limited", 200, `{"errcode":45011,"errmsg":"api minute-quota reach limit"}`, provider.ErrRateLimited, 45011},
		{"system busy", 200, `{"errcode":-1,"errmsg":"system error"}`, provider.ErrUpstream, -1},
		{"bad secret", 200, `{"errcode":40125,"errmsg":"invalid appsecret"}`, provider.ErrUpstream, 40125},
		{"missing openid", 200, `{"session_key":"sk"}`, provider.ErrUpstream, 0},
		{"invalid json", 200, `<html>oops</html>`, provider.ErrUpstream, 0},
		{"non-200", 500, `{"openid":"o"}`, provider.ErrUpstream, 0},
		{"oversized", 200, `{"openid":"o","pad":"` + strings.Repeat("a", maxResponseBytes) + `"}`, provider.ErrUpstream, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}, time.Second)
			_, err := c.Code2Session(context.Background(), testCode)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			var ue *provider.UpstreamError
			if !errors.As(err, &ue) || ue.ErrCode != tt.errCode {
				t.Errorf("errcode = %+v, want %d", ue, tt.errCode)
			}
			assertNoSecrets(t, err)
		})
	}
}

func TestCode2SessionTimeout(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	}, 50*time.Millisecond)
	_, err := c.Code2Session(context.Background(), testCode)
	if !errors.Is(err, provider.ErrUpstreamTimeout) {
		t.Fatalf("err = %v, want ErrUpstreamTimeout", err)
	}
	assertNoSecrets(t, err)
}

func TestCode2SessionContextCanceled(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	_, err := c.Code2Session(ctx, testCode)
	if !errors.Is(err, provider.ErrUpstreamTimeout) {
		t.Fatalf("err = %v, want ErrUpstreamTimeout", err)
	}
	assertNoSecrets(t, err)
}

func TestCode2SessionConnectionRefused(t *testing.T) {
	c := NewClient(testAppID, testSecret, time.Second)
	c.baseURL = "http://127.0.0.1:1"
	_, err := c.Code2Session(context.Background(), testCode)
	if !errors.Is(err, provider.ErrUpstream) {
		t.Fatalf("err = %v, want ErrUpstream", err)
	}
	assertNoSecrets(t, err)
}
