package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"fc_server/internal"
	"fc_server/internal/config"
	"fc_server/internal/processor"
	"fc_server/internal/processor/dto"
	"fc_server/internal/processor/middleware"
	"fc_server/internal/util"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// maxRequestBodyBytes 请求体上限(specs/002 plan §5);好友同步携带整份加密好友列表,单独放宽。
const (
	maxRequestBodyBytes    = 16 << 10
	maxFriendSyncBodyBytes = 256 << 10
)

// 统一响应包裹(specs/001、specs/002 contracts/http-api.md):
// HTTP 状态与 body.code 一致;成功 200,错误码与文案由 processor.ErrorStatus 决定。
func handleError(c *gin.Context, err error) {
	status, msg := processor.ErrorStatus(err)
	switch {
	case status >= http.StatusInternalServerError:
		util.Logger().Error("request failed",
			zap.String("request_id", util.RequestID(c)),
			zap.String("path", c.Request.URL.Path),
			zap.Int("status", status),
			zap.Error(err))
	case status == http.StatusForbidden || status == http.StatusTooManyRequests:
		util.Logger().Warn("request rejected",
			zap.String("request_id", util.RequestID(c)),
			zap.String("path", c.Request.URL.Path),
			zap.Int("status", status),
			zap.Error(err))
	}
	c.JSON(status, gin.H{"code": status, "msg": msg, "data": nil})
}

// recoverPanic 把请求处理中的 panic 写入 error 日志(附调用栈)并返回统一的 500 包裹。
func recoverPanic(c *gin.Context, recovered any) {
	util.Logger().Error("panic recovered",
		zap.String("request_id", util.RequestID(c)),
		zap.String("path", c.Request.URL.Path),
		zap.Any("panic", recovered))
	c.AbortWithStatusJSON(http.StatusInternalServerError,
		gin.H{"code": http.StatusInternalServerError, "msg": "internal server error", "data": nil})
}

func handleSuccess(c *gin.Context, response interface{}) {
	c.JSON(http.StatusOK, gin.H{"code": http.StatusOK, "msg": "", "data": response})
}

// bindJSON 解析请求体;超限返回 413,格式错误返回 400。
func bindJSON(c *gin.Context, request interface{}) bool {
	if err := c.ShouldBindJSON(request); err != nil {
		handleBodyError(c, err)
		return false
	}
	return true
}

// readBody 读取原始请求体;超限返回 413。
func readBody(c *gin.Context) ([]byte, bool) {
	body, err := c.GetRawData()
	if err != nil {
		handleBodyError(c, err)
		return nil, false
	}
	return body, true
}

func handleBodyError(c *gin.Context, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		handleError(c, processor.ErrBodyTooLarge)
		return
	}
	c.JSON(http.StatusBadRequest, gin.H{"code": http.StatusBadRequest, "msg": "invalid request body: " + err.Error(), "data": nil})
}

// identity 取认证中间件写入的玩家身份;路由配置错误时按未认证处理而不是信任请求体。
func identity(c *gin.Context) (middleware.Identity, bool) {
	id, ok := middleware.CurrentIdentity(c)
	if !ok {
		handleError(c, errors.New("route is missing auth middleware"))
	}
	return id, ok
}

func newRouter(cfg *config.Config) *gin.Engine {
	r := gin.New()
	if err := r.SetTrustedProxies(cfg.TrustedProxies); err != nil {
		panic("invalid TRUSTED_PROXIES: " + err.Error())
	}
	r.Use(
		middleware.LimitBody(maxRequestBodyBytes, map[string]int64{"/friends/sync": maxFriendSyncBodyBytes}),
		util.RequestResponseLogger(),
		gin.CustomRecoveryWithWriter(io.Discard, recoverPanic),
		middleware.CORS(cfg.CORSAllowOrigins),
	)
	requireAuth := middleware.RequireAuth(handleError)

	// 接口契约见 specs/002-wechat-login/contracts/http-api.md;字段与客户端要求见各请求/响应 DTO 的注释。
	// 除 POST /auth/wechat 外均需 `Authorization: Bearer <token>`;401 时客户端重新 wx.login 登录,最多自动重试一次。

	// POST /auth/wechat:用 wx.login 的 code 登录(dto.WechatLoginRequest)。code 只能用一次、5 分钟内有效,
	// 失败重试必须重新调用 wx.login 取新 code;登录不会触发头像授权弹窗。
	authGroup := r.Group("/auth", middleware.NoStore())
	authGroup.POST("/wechat", func(c *gin.Context) {
		request := &dto.WechatLoginRequest{}
		if !bindJSON(c, request) {
			return
		}
		resp, err := processor.WechatLogin(c.Request.Context(), request, c.ClientIP())
		if err != nil {
			handleError(c, err)
			return
		}
		handleSuccess(c, resp)
	})
	// POST /auth/logout:只注销当前 token;不影响其他设备,不清理本地存档与头像授权。
	authGroup.POST("/logout", requireAuth, func(c *gin.Context) {
		id, ok := identity(c)
		if !ok {
			return
		}
		if err := processor.Logout(c.Request.Context(), id.Token); err != nil {
			handleError(c, err)
			return
		}
		handleSuccess(c, nil)
	})

	// GET /users/me:当前玩家资料(dto.UserDto)。PATCH /users/me/profile:玩家授权后立即保存头像昵称
	// (dto.ProfileDto),用于刷新游戏内头部显示;打完一局时也可以直接随 /upload_score 的 profile 提交。
	users := r.Group("/users", middleware.NoStore(), requireAuth)
	users.GET("/me", func(c *gin.Context) {
		id, ok := identity(c)
		if !ok {
			return
		}
		resp, err := processor.GetCurrentUser(c.Request.Context(), id.UserID)
		if err != nil {
			handleError(c, err)
			return
		}
		handleSuccess(c, resp)
	})
	users.PATCH("/me/profile", func(c *gin.Context) {
		id, ok := identity(c)
		if !ok {
			return
		}
		request := &dto.ProfileDto{}
		if !bindJSON(c, request) {
			return
		}
		resp, err := processor.UpdateProfile(c.Request.Context(), id.UserID, request)
		if err != nil {
			handleError(c, err)
			return
		}
		handleSuccess(c, resp)
	})

	// POST /friends/sync:提交 wx.getRelationFriendList 的加密数据,服务端解密并保存好友列表,
	// 用于人榜(dto.FriendSyncRequest 中有完整的客户端集成步骤)。
	r.POST("/friends/sync", middleware.NoStore(), requireAuth, func(c *gin.Context) {
		id, ok := identity(c)
		if !ok {
			return
		}
		request := &dto.FriendSyncRequest{}
		if !bindJSON(c, request) {
			return
		}
		resp, err := processor.SyncFriends(c.Request.Context(), id.UserID, request)
		if err != nil {
			handleError(c, err)
			return
		}
		handleSuccess(c, resp)
	})

	// POST /fetch_rank:天榜(全国)、地榜(城市/省份)、人榜(好友),各含总/月/周三个窗口,
	// 成员带当前昵称与 avatar_url(dto.FetchRankListRequest / dto.ScoreMetaDto)。
	r.POST("/fetch_rank", requireAuth, func(c *gin.Context) {
		id, ok := identity(c)
		if !ok {
			return
		}
		request := &dto.FetchRankListRequest{}
		if !bindJSON(c, request) {
			return
		}
		resp, err := processor.FetchRankList(c.Request.Context(), id.UserID, request)
		if err != nil {
			handleError(c, err)
			return
		}
		handleSuccess(c, resp)
	})

	// POST /runs/start:开一局普通局时领取对局票(种子由服务端下发),结束时凭 run_id 上报成绩
	// (dto.StartRunResponse)。梦蝶局不领票、不上报。
	r.POST("/runs/start", requireAuth, func(c *gin.Context) {
		id, ok := identity(c)
		if !ok {
			return
		}
		resp, err := processor.StartRun(c.Request.Context(), id.UserID)
		if err != nil {
			handleError(c, err)
			return
		}
		handleSuccess(c, resp)
	})

	// POST /upload_score:上报一局成绩(关卡数、最后一关修为、修为总数),可同时提交授权后的头像昵称
	// (dto.UploadScoreRequest)。
	r.POST("/upload_score", requireAuth, func(c *gin.Context) {
		id, ok := identity(c)
		if !ok {
			return
		}
		request := &dto.UploadScoreRequest{}
		if !bindJSON(c, request) {
			return
		}
		resp, err := processor.UploadScore(c.Request.Context(), id.UserID, c.ClientIP(), request)
		if err != nil {
			handleError(c, err)
			return
		}
		handleSuccess(c, resp)
	})

	// POST /upload_game_record:追加一条玩家游戏记录,请求体即记录本身(任意 JSON 对象,见 dto/game_record.go)。
	// 记录先进 Redis 队列,由后台任务异步批量写入 MySQL player_game_record(按 user_id 索引,一人多条)。
	r.POST("/upload_game_record", requireAuth, func(c *gin.Context) {
		id, ok := identity(c)
		if !ok {
			return
		}
		body, ok := readBody(c)
		if !ok {
			return
		}
		resp, err := processor.UploadGameRecord(c.Request.Context(), id.UserID, c.ClientIP(), body)
		if err != nil {
			handleError(c, err)
			return
		}
		handleSuccess(c, resp)
	})

	// POST /upload_achievement:记录一项成就的首次解锁,结构化请求见 dto.UploadAchievementRequest。
	// 先写 Redis(判重与待持久化队列),后台任务异步写入 MySQL player_achievement。
	r.POST("/upload_achievement", requireAuth, func(c *gin.Context) {
		id, ok := identity(c)
		if !ok {
			return
		}
		request := &dto.UploadAchievementRequest{}
		if !bindJSON(c, request) {
			return
		}
		resp, err := processor.UploadAchievement(c.Request.Context(), id.UserID, c.ClientIP(), request)
		if err != nil {
			handleError(c, err)
			return
		}
		handleSuccess(c, resp)
	})

	return r
}

// localEnvFile 本机配置文件,存在时启动前读入(真实环境变量优先);容器镜像中不包含它。
const localEnvFile = ".env.local"

func main() {
	util.InitLogger()
	if err := config.LoadDotEnv(localEnvFile); err != nil {
		util.Logger().Fatal("load "+localEnvFile+" failed", zap.Error(err))
	}
	cfg := config.Load()
	if os.Getenv(gin.EnvGinMode) == "" {
		gin.SetMode(gin.ReleaseMode)
	}
	app := internal.Init(cfg)
	defer util.CloseLogger()

	srv := &http.Server{Addr: ":" + cfg.Port, Handler: newRouter(cfg)}
	go func() {
		util.Logger().Info("server listening", zap.String("port", cfg.Port))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			util.Logger().Fatal("server failed", zap.Error(err))
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	util.Logger().Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	app.Shutdown()
}
