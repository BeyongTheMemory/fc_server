package middleware

import (
	"strings"

	"fc_server/internal/domain/person/application"
	"fc_server/internal/domain/person/application/impl"

	"github.com/gin-gonic/gin"
)

const identityKey = "fc.auth.identity"

// Identity 通过认证的调用方;下游只能从这里取玩家身份,不能信任请求体。
type Identity struct {
	UserID string
	Token  string
}

// RequireAuth 校验 `Authorization: Bearer <token>`,失败时交给 onError 输出统一错误包裹并中止。
func RequireAuth(onError func(*gin.Context, error)) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, ok := ParseBearer(c.GetHeader("Authorization"))
		if !ok {
			onError(c, application.ErrUnauthenticated)
			c.Abort()
			return
		}
		session, err := impl.GetUserApplicationImpl().Authenticate(c.Request.Context(), token)
		if err != nil {
			onError(c, err)
			c.Abort()
			return
		}
		c.Set(identityKey, Identity{UserID: session.UserID, Token: token})
		c.Next()
	}
}

// CurrentIdentity 取 RequireAuth 写入的身份;未经认证的路由上返回 false。
func CurrentIdentity(c *gin.Context) (Identity, bool) {
	v, ok := c.Get(identityKey)
	if !ok {
		return Identity{}, false
	}
	id, ok := v.(Identity)
	return id, ok && id.UserID != ""
}

// ParseBearer 解析 Bearer 凭据(scheme 不区分大小写)。
func ParseBearer(header string) (string, bool) {
	scheme, token, found := strings.Cut(strings.TrimSpace(header), " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}
