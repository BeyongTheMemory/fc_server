package util

import (
	"bytes"
	"io"
	"log"
	"time"

	"github.com/gin-gonic/gin"
)

// 捕获响应体
type bodyWriter struct {
	gin.ResponseWriter
	body *bytes.Buffer
}

func (w bodyWriter) Write(b []byte) (int, error) {
	w.body.Write(b) // 记录响应体
	return w.ResponseWriter.Write(b)
}

func RequestResponseLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		// --- 捕获请求体 ---
		var requestBody []byte
		if c.Request.Body != nil {
			requestBody, _ = io.ReadAll(c.Request.Body)
			// 重要：重新赋值回去，否则 handler 里读取不到
			c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
		}

		// --- 捕获响应体 ---
		blw := &bodyWriter{body: bytes.NewBufferString(""), ResponseWriter: c.Writer}
		c.Writer = blw

		// 开始计时
		start := time.Now()

		// 执行 handler
		c.Next()

		// --- 日志输出 ---
		duration := time.Since(start)
		method := c.Request.Method
		path := c.Request.URL.Path
		status := c.Writer.Status()
		clientIP := c.ClientIP()

		log.Printf("\n[LOG] %v | %3d | %13v | %-7s %-20s | IP: %s\n | req:%s | resp: %s",
			time.Now().Format("2006-01-02 15:04:05"),
			status,
			duration,
			method,
			path,
			clientIP,
			string(requestBody),
			blw.body.String(),
		)
	}
}
