package util

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const (
	RequestIDHeader    = "X-Request-Id"
	requestIDKey       = "fc.request_id"
	maxRequestIDLength = 64
)

// RequestResponseLogger 每个请求记录一行元数据日志。
// 刻意不记录请求体、响应体、查询串与请求头:其中可能含登录 code、业务 token、
// Authorization 或玩家资料原文(specs/002 plan §7)。
func RequestResponseLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		reqID := sanitizeRequestID(c.GetHeader(RequestIDHeader))
		if reqID == "" {
			reqID = newRequestID()
		}
		c.Set(requestIDKey, reqID)
		c.Header(RequestIDHeader, reqID)

		c.Next()

		Logger().Info("http request",
			zap.String("request_id", reqID),
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.Int("status", c.Writer.Status()),
			zap.Duration("latency", time.Since(start)),
			zap.Int("resp_bytes", c.Writer.Size()),
			zap.String("client_ip", c.ClientIP()),
		)
	}
}

// RequestID 当前请求的请求 ID,供边界错误日志关联。
func RequestID(c *gin.Context) string {
	return c.GetString(requestIDKey)
}

// sanitizeRequestID 只接受短的 [A-Za-z0-9_-] 请求 ID,避免把任意客户端内容写进日志。
func sanitizeRequestID(id string) string {
	if id == "" || len(id) > maxRequestIDLength {
		return ""
	}
	for _, r := range id {
		if !(r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return ""
		}
	}
	return id
}

func newRequestID() string {
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}
