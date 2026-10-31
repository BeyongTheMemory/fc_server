package sync

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"fc_server/internal/domain/achievement/entity"
	"fc_server/internal/infrastructure/repository_impl"
	"fc_server/internal/util"

	"go.uber.org/zap"
)

// player_achievement 每个玩家每项成就一行,(user_id, achievement_id) 唯一,保留首次解锁。
const createAchievementTableDDL = `
CREATE TABLE IF NOT EXISTS player_achievement (
  id             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id        VARCHAR(64)     NOT NULL,
  achievement_id VARCHAR(64)     NOT NULL,
  unlocked_at_ms BIGINT          NOT NULL,
  received_at_ms BIGINT          NOT NULL,
  detail         JSON            NULL,
  client_ip      VARCHAR(45)     NOT NULL DEFAULT '',
  PRIMARY KEY (id),
  UNIQUE KEY uk_user_achievement (user_id, achievement_id),
  KEY idx_achievement (achievement_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`

// 已存在时不覆盖:首次解锁为准,队列重放也是幂等的。
const insertAchievementSQL = `
INSERT INTO player_achievement (user_id, achievement_id, unlocked_at_ms, received_at_ms, detail, client_ip)
VALUES (?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE user_id = user_id`

// AchievementSyncer 把 Redis 队列中的首次解锁记录批量写入 MySQL(流程见 drainQueue)。
type AchievementSyncer struct {
	db          *sql.DB // nil 表示停用
	storage     *repository_impl.AchievementRedisStorage
	interval    time.Duration
	schemaReady bool
	started     bool
	stopCh      chan struct{}
	doneCh      chan struct{}
}

func NewAchievementSyncer(db *sql.DB, storage *repository_impl.AchievementRedisStorage, interval time.Duration) *AchievementSyncer {
	return &AchievementSyncer{
		db:       db,
		storage:  storage,
		interval: interval,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
}

func (s *AchievementSyncer) Enabled() bool {
	return s.db != nil
}

func (s *AchievementSyncer) Start() {
	if !s.Enabled() {
		util.Logger().Warn("achievement persistence disabled: MYSQL_DSN not set; unlocks live only in redis")
		return
	}
	s.started = true
	go s.run()
}

func (s *AchievementSyncer) run() {
	defer close(s.doneCh)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.syncRound(context.Background())
		case <-s.stopCh:
			s.syncRound(context.Background())
			return
		}
	}
}

// Stop 停止循环并等待最后一轮 flush 完成。
func (s *AchievementSyncer) Stop() {
	if !s.started {
		return
	}
	close(s.stopCh)
	<-s.doneCh
}

func (s *AchievementSyncer) ensureSchema(ctx context.Context) error {
	if s.schemaReady {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, createAchievementTableDDL); err != nil {
		return fmt.Errorf("ensure achievement schema: %w", err)
	}
	s.schemaReady = true
	return nil
}

func (s *AchievementSyncer) syncRound(ctx context.Context) {
	if err := s.ensureSchema(ctx); err != nil {
		util.Logger().Error("achievement sync: schema not ready", zap.Error(err))
		return
	}
	drainQueue(ctx, "achievement", s.storage, s.insertBatch)
}

func (s *AchievementSyncer) insertBatch(ctx context.Context, items []*entity.Achievement) (int, error) {
	return insertRows(ctx, s.db, "achievement", insertAchievementSQL, len(items),
		func(i int) []any {
			a := items[i]
			var detail any // nil → SQL NULL
			if len(a.Detail) > 0 {
				detail = string(a.Detail)
			}
			return []any{a.UserID, a.AchievementID, a.UnlockedAtMs, a.ReceivedAtMs, detail, a.ClientIP}
		},
		func(i int) []zap.Field {
			return []zap.Field{zap.String("user_id", items[i].UserID), zap.String("achievement_id", items[i].AchievementID)}
		})
}
