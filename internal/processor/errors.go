package processor

import (
	"errors"
	"net/http"

	achievementapp "fc_server/internal/domain/achievement/application"
	"fc_server/internal/domain/person/application"
	"fc_server/internal/domain/person/provider"
	recordapp "fc_server/internal/domain/record/application"
	runapp "fc_server/internal/domain/run/application"
)

// ErrBodyTooLarge 请求体超过上限,HTTP 层返回 413。
var ErrBodyTooLarge = errors.New("request body too large")

// ErrorStatus 把错误映射为 HTTP 状态码与对客户端安全的提示(specs/002 plan §5 错误行为)。
// 只有参数类错误回显原文;认证、上游与存储错误一律使用固定文案,细节只写服务端日志。
func ErrorStatus(err error) (int, string) {
	switch {
	case errors.Is(err, ErrInvalidParam), errors.Is(err, application.ErrInvalidProfile):
		return http.StatusBadRequest, err.Error()
	case errors.Is(err, provider.ErrInvalidEncryptedData):
		return http.StatusBadRequest, "invalid encrypted data, retry with a fresh wx.login code"
	case errors.Is(err, application.ErrInvalidFriendData):
		return http.StatusBadRequest, "friend data does not belong to current player"
	case errors.Is(err, runapp.ErrRunNotFound), errors.Is(err, runapp.ErrRunTooFast):
		return http.StatusBadRequest, err.Error()
	case errors.Is(err, runapp.ErrRunConflict):
		return http.StatusConflict, err.Error()
	case errors.Is(err, runapp.ErrTooManyRuns):
		return http.StatusTooManyRequests, "too many runs started, retry later"
	case errors.Is(err, ErrBodyTooLarge):
		return http.StatusRequestEntityTooLarge, "request body too large"
	case errors.Is(err, provider.ErrInvalidCode):
		return http.StatusUnauthorized, "invalid or expired login code"
	case errors.Is(err, application.ErrUnauthenticated):
		return http.StatusUnauthorized, "unauthorized"
	case errors.Is(err, provider.ErrRiskBlocked):
		return http.StatusForbidden, "login is temporarily unavailable"
	case errors.Is(err, provider.ErrRateLimited), errors.Is(err, application.ErrTooManyAttempts):
		return http.StatusTooManyRequests, "too many requests, retry later"
	case errors.Is(err, provider.ErrUpstreamTimeout):
		return http.StatusGatewayTimeout, "upstream timeout"
	case errors.Is(err, provider.ErrUpstream):
		return http.StatusBadGateway, "upstream service error"
	case errors.Is(err, application.ErrUnavailable), errors.Is(err, recordapp.ErrUnavailable),
		errors.Is(err, achievementapp.ErrUnavailable), errors.Is(err, runapp.ErrUnavailable):
		return http.StatusServiceUnavailable, "service unavailable"
	default:
		return http.StatusInternalServerError, "internal server error"
	}
}
