package internal

import (
	"context"
	"time"

	"fc_server/internal/config"
	achievementimpl "fc_server/internal/domain/achievement/application/impl"
	personimpl "fc_server/internal/domain/person/application/impl"
	"fc_server/internal/domain/rank/application/impl"
	recordimpl "fc_server/internal/domain/record/application/impl"
	runimpl "fc_server/internal/domain/run/application/impl"
	"fc_server/internal/infrastructure/repository_impl"
	ranksync "fc_server/internal/infrastructure/sync"
	"fc_server/internal/infrastructure/wechat"
	"fc_server/internal/processor"
	"fc_server/internal/util"

	"go.uber.org/zap"
)

// App 聚合需要生命周期管理的组件。
type App struct {
	Syncer            *ranksync.Syncer
	PlayerSyncer      *ranksync.PlayerSyncer
	GameRecordSyncer  *ranksync.GameRecordSyncer
	AchievementSyncer *ranksync.AchievementSyncer
}

func Init(cfg *config.Config) *App {
	util.InitLogger()
	// 启动阶段的 panic(配置非法、Redis 连不上等)写入 error 日志后退出,而不是只打到 stderr。
	defer func() {
		if r := recover(); r != nil {
			util.Logger().Fatal("startup failed", zap.Any("error", r))
		}
	}()
	if err := util.SetupLogger(util.LogOptions{
		Dir:           cfg.LogDir,
		Console:       cfg.LogConsole,
		RetentionDays: cfg.LogRetentionDays,
	}); err != nil {
		panic("Failed to set up logging: " + err.Error())
	}
	if err := cfg.Validate(); err != nil {
		panic("Invalid config: " + err.Error())
	}

	repository_impl.Init(cfg)
	storage := repository_impl.GetRankRedisStorage()
	impl.Init(storage, cfg.RankLimit, cfg.RegionMinPlayers)
	processor.Init(cfg.MaxStage, cfg.LeaderboardHidden)

	redisClient := storage.Client()
	playerStorage := repository_impl.NewPlayerRedisStorage(redisClient)
	personimpl.Init(
		wechat.NewClient(cfg.WechatAppID, cfg.WechatAppSecret, cfg.WechatHTTPTimeout),
		playerStorage,
		repository_impl.NewSessionRedisStorage(redisClient),
		repository_impl.NewRateLimiterRedis(redisClient),
		repository_impl.NewFriendRedisStorage(redisClient),
		personimpl.Options{
			AppID:           cfg.WechatAppID,
			SessionTTL:      cfg.AuthSessionTTL,
			LoginRateLimit:  cfg.LoginRateLimit,
			LoginRateWindow: cfg.LoginRateWindow,
			FriendTTL:       30 * 24 * time.Hour,
			MaxFriends:      1000,
		},
	)

	runimpl.Init(
		repository_impl.NewRunRedisStorage(redisClient),
		repository_impl.NewRateLimiterRedis(redisClient),
		runimpl.Options{
			TicketTTL:     cfg.RunTicketTTL,
			StartLimit:    cfg.RunStartLimit,
			StartWindow:   time.Hour,
			MinMsPerStage: int64(cfg.MinSecondsPerStage) * 1000,
		},
	)

	gameRecordQueue := repository_impl.NewGameRecordRedisQueue(redisClient, cfg.GameRecordQueueMax)
	recordimpl.Init(gameRecordQueue)
	achievementStorage := repository_impl.NewAchievementRedisStorage(redisClient, cfg.GameRecordQueueMax)
	achievementimpl.Init(achievementStorage)

	syncer, err := ranksync.New(cfg, storage)
	if err != nil {
		panic("Failed to init MySQL syncer: " + err.Error())
	}
	playerSyncer := ranksync.NewPlayerSyncer(syncer.DB(), playerStorage, cfg.RankSyncInterval)
	gameRecordSyncer := ranksync.NewGameRecordSyncer(syncer.DB(), gameRecordQueue, cfg.RankSyncInterval)
	achievementSyncer := ranksync.NewAchievementSyncer(syncer.DB(), achievementStorage, cfg.RankSyncInterval)

	ensureCtx, cancelEnsure := context.WithTimeout(context.Background(), 10*time.Second)
	if err := syncer.EnsureDatabase(ensureCtx); err != nil {
		util.Logger().Warn("could not ensure mysql database exists; backups will retry once it does", zap.Error(err))
	}
	cancelEnsure()

	// 恢复必须在开放 HTTP 之前完成(main 在 Init 返回后才监听端口)。
	if cfg.RebuildRankFromDB {
		count, err := syncer.RebuildFromDB(context.Background())
		if err != nil {
			panic("Failed to rebuild rank from MySQL: " + err.Error())
		}
		util.Logger().Info("rank rebuilt from MySQL", zap.Int("players", count))
	}
	if cfg.RebuildPlayerFromDB {
		count, err := playerSyncer.RebuildFromDB(context.Background())
		if err != nil {
			panic("Failed to rebuild player profiles from MySQL: " + err.Error())
		}
		util.Logger().Info("player profiles rebuilt from MySQL", zap.Int("players", count))
	}
	syncer.Start()
	playerSyncer.Start()
	gameRecordSyncer.Start()
	achievementSyncer.Start()

	return &App{Syncer: syncer, PlayerSyncer: playerSyncer, GameRecordSyncer: gameRecordSyncer, AchievementSyncer: achievementSyncer}
}

// Shutdown 按依赖逆序释放组件:先停各同步器(各自最后 flush 一轮),再关闭共享的数据库连接。
func (a *App) Shutdown() {
	if a.AchievementSyncer != nil {
		a.AchievementSyncer.Stop()
	}
	if a.GameRecordSyncer != nil {
		a.GameRecordSyncer.Stop()
	}
	if a.PlayerSyncer != nil {
		a.PlayerSyncer.Stop()
	}
	if a.Syncer != nil {
		a.Syncer.Stop()
		if err := a.Syncer.Close(); err != nil {
			util.Logger().Warn("close mysql failed", zap.Error(err))
		}
	}
}
