package sync

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"fc_server/internal/domain/record/entity"
	"fc_server/internal/infrastructure/repository_impl"
	"fc_server/internal/util"

	"go.uber.org/zap"
)

// player_game_record 只追加;record_id 唯一键保证队列重放(如提交后、删除队头前崩溃)不会重复插入。
const createGameRecordTableDDL = `
CREATE TABLE IF NOT EXISTS player_game_record (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  record_id     CHAR(32)        NOT NULL,
  user_id       VARCHAR(64)     NOT NULL,
  data          JSON            NOT NULL,
  created_at_ms BIGINT          NOT NULL,
  client_ip     VARCHAR(45)     NOT NULL DEFAULT '',
  PRIMARY KEY (id),
  UNIQUE KEY uk_record_id (record_id),
  KEY idx_user_created (user_id, created_at_ms)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`

// 不用 INSERT IGNORE:它会把数据错误降级为警告并写入残缺数据。
const insertGameRecordSQL = `
INSERT INTO player_game_record (record_id, user_id, data, created_at_ms, client_ip)
VALUES (?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE record_id = record_id`

// GameRecordSyncer 把 Redis 队列中的游戏记录批量追加进 MySQL(流程见 drainQueue)。
type GameRecordSyncer struct {
	db          *sql.DB // nil 表示停用
	queue       *repository_impl.GameRecordRedisQueue
	interval    time.Duration
	schemaReady bool
	started     bool
	stopCh      chan struct{}
	doneCh      chan struct{}
}

func NewGameRecordSyncer(db *sql.DB, queue *repository_impl.GameRecordRedisQueue, interval time.Duration) *GameRecordSyncer {
	return &GameRecordSyncer{
		db:       db,
		queue:    queue,
		interval: interval,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
}

func (s *GameRecordSyncer) Enabled() bool {
	return s.db != nil
}

func (s *GameRecordSyncer) Start() {
	if !s.Enabled() {
		util.Logger().Warn("game record persistence disabled: MYSQL_DSN not set; records accumulate in redis up to GAME_RECORD_QUEUE_MAX")
		return
	}
	s.started = true
	go s.run()
}

func (s *GameRecordSyncer) run() {
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
func (s *GameRecordSyncer) Stop() {
	if !s.started {
		return
	}
	close(s.stopCh)
	<-s.doneCh
}

func (s *GameRecordSyncer) ensureSchema(ctx context.Context) error {
	if s.schemaReady {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, createGameRecordTableDDL); err != nil {
		return fmt.Errorf("ensure game record schema: %w", err)
	}
	s.schemaReady = true
	return nil
}

func (s *GameRecordSyncer) syncRound(ctx context.Context) {
	if err := s.ensureSchema(ctx); err != nil {
		util.Logger().Error("game record sync: schema not ready", zap.Error(err))
		return
	}
	drainQueue(ctx, "game record", s.queue, s.insertBatch)
}

// insertBatch 在一个事务中追加一批记录,返回因内容非法被丢弃的条数。
func (s *GameRecordSyncer) insertBatch(ctx context.Context, records []*entity.GameRecord) (int, error) {
	return insertRows(ctx, s.db, "game record", insertGameRecordSQL, len(records),
		func(i int) []any {
			r := records[i]
			return []any{r.ID, r.UserID, string(r.Data), r.CreatedAtMs, r.ClientIP}
		},
		func(i int) []zap.Field {
			return []zap.Field{zap.String("record_id", records[i].ID), zap.String("user_id", records[i].UserID)}
		})
}
