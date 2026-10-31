package processor

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"fc_server/internal/domain/person/application"
	"fc_server/internal/domain/person/provider"
	"fc_server/internal/processor/dto"
)

func TestWechatLoginValidation(t *testing.T) {
	setupPlayers(t)
	for name, code := range map[string]string{
		"empty":      "",
		"too long":   strings.Repeat("a", 1025),
		"whitespace": "abc def",
		"control":    "abc\x00",
	} {
		if _, err := WechatLogin(context.Background(), &dto.WechatLoginRequest{Code: code}, ""); !errors.Is(err, ErrInvalidParam) {
			t.Errorf("%s code: err = %v, want ErrInvalidParam", name, err)
		}
	}
	if _, err := WechatLogin(context.Background(), nil, ""); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("nil request: err = %v, want ErrInvalidParam", err)
	}
}

func TestCurrentUserAndUpdateProfileMapping(t *testing.T) {
	setupPlayers(t)
	ctx := context.Background()

	me, err := GetCurrentUser(ctx, unnamedUser)
	if err != nil || me.UserId != unnamedUser || me.Nickname != "无名" || me.AvatarUrl != "" {
		t.Fatalf("GetCurrentUser = (%+v, %v)", me, err)
	}
	me, err = UpdateProfile(ctx, unnamedUser, &dto.ProfileDto{Nickname: "新名字", AvatarUrl: "https://thirdwx.qlogo.cn/x"})
	if err != nil || me.Nickname != "新名字" || me.AvatarUrl != "https://thirdwx.qlogo.cn/x" {
		t.Fatalf("UpdateProfile = (%+v, %v)", me, err)
	}
	if _, err := UpdateProfile(ctx, unnamedUser, &dto.ProfileDto{Nickname: ""}); !errors.Is(err, application.ErrInvalidProfile) {
		t.Errorf("empty nickname err = %v, want ErrInvalidProfile", err)
	}
	if _, err := UpdateProfile(ctx, unnamedUser, nil); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("nil request err = %v, want ErrInvalidParam", err)
	}
}

func TestErrorStatusMapping(t *testing.T) {
	tests := []struct {
		err    error
		status int
	}{
		{ErrInvalidParam, http.StatusBadRequest},
		{application.ErrInvalidProfile, http.StatusBadRequest},
		{ErrBodyTooLarge, http.StatusRequestEntityTooLarge},
		{application.ErrUnauthenticated, http.StatusUnauthorized},
		{&provider.UpstreamError{Kind: provider.ErrInvalidCode, ErrCode: 40029}, http.StatusUnauthorized},
		{&provider.UpstreamError{Kind: provider.ErrRiskBlocked, ErrCode: 40226}, http.StatusForbidden},
		{&provider.UpstreamError{Kind: provider.ErrRateLimited, ErrCode: 45011}, http.StatusTooManyRequests},
		{application.ErrTooManyAttempts, http.StatusTooManyRequests},
		{&provider.UpstreamError{Kind: provider.ErrUpstream, ErrCode: 40125}, http.StatusBadGateway},
		{&provider.UpstreamError{Kind: provider.ErrUpstreamTimeout}, http.StatusGatewayTimeout},
		{&provider.UpstreamError{Kind: provider.ErrInvalidEncryptedData}, http.StatusBadRequest},
		{application.ErrInvalidFriendData, http.StatusBadRequest},
		{application.ErrUnavailable, http.StatusServiceUnavailable},
		{errors.New("boom"), http.StatusInternalServerError},
	}
	for _, tt := range tests {
		status, msg := ErrorStatus(tt.err)
		if status != tt.status {
			t.Errorf("ErrorStatus(%v) = %d, want %d", tt.err, status, tt.status)
		}
		if status >= 500 && strings.Contains(msg, "errcode") {
			t.Errorf("5xx message leaks upstream details: %q", msg)
		}
	}
}

func TestSyncFriendsValidation(t *testing.T) {
	setupPlayers(t)
	for name, req := range map[string]*dto.FriendSyncRequest{
		"nil":          nil,
		"missing code": {EncryptedData: "e", Iv: "i"},
		"missing data": {Code: "c", Iv: "i"},
		"missing iv":   {Code: "c", EncryptedData: "e"},
		"long iv":      {Code: "c", EncryptedData: "e", Iv: strings.Repeat("i", 65)},
	} {
		if _, err := SyncFriends(context.Background(), testUserID, req); !errors.Is(err, ErrInvalidParam) {
			t.Errorf("%s: err = %v, want ErrInvalidParam", name, err)
		}
	}
}
