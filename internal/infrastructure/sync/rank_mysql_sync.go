// Package sync 将 Redis 中的玩家最高分增量定期同步到 MySQL 持久备份,
// 并支持从 MySQL 全量重建 Redis 榜单(specs/001 research.md R5~R8)。
// MySQL 不在在线请求路径上;MYSQL_DSN 未配置时同步器处于停用态。
package sync

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"time"

	"fc_server/internal/config"
	"fc_server/internal/domain/rank/entity/vo"
	"fc_server/internal/infrastructure/repository_impl"
	"fc_server/internal/util"

	"github.com/go-sql-driver/mysql"
	"go.uber.org/zap"
)

// 修为可超过任何整数类型,以十进制字符串保存;score_key 是三项成绩的有序编码
// (与 Redis 成员前缀相同,见 repository_impl.ScoreKey),按字节比较即按成绩比较。
const createTableDDL = `
CREATE TABLE IF NOT EXISTS user_best_score (
  user_id           VARCHAR(64)  NOT NULL,
  user_name         VARCHAR(64)  NOT NULL DEFAULT '',
  stage             BIGINT       NOT NULL,
  stage_cultivation VARCHAR(320) CHARACTER SET ascii NOT NULL,
  total_cultivation VARCHAR(320) CHARACTER SET ascii NOT NULL,
  score_key         VARCHAR(700) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  achieved_at       BIGINT       NOT NULL,
  client_ip         VARCHAR(45)  NOT NULL DEFAULT '',
  province          VARCHAR(64)  NOT NULL DEFAULT '',
  city              VARCHAR(64)  NOT NULL DEFAULT '',
  district          VARCHAR(64)  NOT NULL DEFAULT '',
  updated_at        TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP
                                 ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (user_id),
  KEY idx_region_score (province, city, district, score_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`

// upsertSQL 只接受更好的成绩,乱序批次不会回退成绩与其元数据(data-model.md)。
// score_key 必须最后赋值:前面各列的 IF 比较的仍是旧 score_key。
const upsertSQL = `
INSERT INTO user_best_score
  (user_id, user_name, stage, stage_cultivation, total_cultivation, score_key,
   achieved_at, client_ip, province, city, district)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
  user_name         = IF(VALUES(score_key) >= score_key, VALUES(user_name), user_name),
  stage             = IF(VALUES(score_key) >  score_key, VALUES(stage), stage),
  stage_cultivation = IF(VALUES(score_key) >  score_key, VALUES(stage_cultivation), stage_cultivation),
  total_cultivation = IF(VALUES(score_key) >  score_key, VALUES(total_cultivation), total_cultivation),
  achieved_at       = IF(VALUES(score_key) >  score_key, VALUES(achieved_at), achieved_at),
  client_ip         = IF(VALUES(score_key) >  score_key, VALUES(client_ip), client_ip),
  province          = IF(VALUES(score_key) >= score_key, VALUES(province), province),
  city              = IF(VALUES(score_key) >= score_key, VALUES(city), city),
  district          = IF(VALUES(score_key) >= score_key, VALUES(district), district),
  score_key         = GREATEST(score_key, VALUES(score_key))`

const popBatchSize = 128

type Syncer struct {
	db          *sql.DB // nil 表示停用(MYSQL_DSN 未配置)
	dsn         string
	storage     *repository_impl.RankRedisStorage
	interval    time.Duration
	schemaReady bool
	started     bool
	stopCh      chan struct{}
	doneCh      chan struct{}
}

func New(cfg *config.Config, storage *repository_impl.RankRedisStorage) (*Syncer, error) {
	s := &Syncer{
		storage:  storage,
		interval: cfg.RankSyncInterval,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
	if cfg.MySQLDSN == "" {
		return s, nil
	}
	db, err := sql.Open("mysql", cfg.MySQLDSN)
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(time.Hour)
	s.db = db
	s.dsn = cfg.MySQLDSN
	return s, nil
}

var databaseNamePattern = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// EnsureDatabase DSN 中指定的库不存在时创建(utf8mb4),用于云托管等只注入连接信息的环境;
// 停用态或 DSN 未指定库名时什么也不做。
func (s *Syncer) EnsureDatabase(ctx context.Context) error {
	if !s.Enabled() {
		return nil
	}
	mc, err := mysql.ParseDSN(s.dsn)
	if err != nil {
		return fmt.Errorf("parse mysql dsn: %w", err)
	}
	name := mc.DBName
	if name == "" {
		return nil
	}
	if !databaseNamePattern.MatchString(name) {
		return fmt.Errorf("database name %q must contain only letters, digits and underscores", name)
	}
	mc.DBName = ""
	server, err := sql.Open("mysql", mc.FormatDSN())
	if err != nil {
		return fmt.Errorf("open mysql server: %w", err)
	}
	defer server.Close()
	if _, err := server.ExecContext(ctx, "CREATE DATABASE IF NOT EXISTS `"+name+"` DEFAULT CHARACTER SET utf8mb4"); err != nil {
		return fmt.Errorf("create database %s: %w", name, err)
	}
	return nil
}

func (s *Syncer) Enabled() bool {
	return s.db != nil
}

// DB 共享给其它备份任务(如玩家资料同步)的连接池;停用态为 nil。
func (s *Syncer) DB() *sql.DB {
	return s.db
}

// Close 释放数据库连接池,须在所有同步任务停止后调用。
func (s *Syncer) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Start 启动后台同步循环;停用态只打一条 Warn(research.md R7)。
func (s *Syncer) Start() {
	if !s.Enabled() {
		util.Logger().Warn("MySQL sync disabled: MYSQL_DSN not set; dirty players stay queued in redis")
		return
	}
	s.started = true
	go s.run()
}

func (s *Syncer) run() {
	defer close(s.doneCh)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.syncRound(context.Background())
		case <-s.stopCh:
			// 优雅停机:退出前最后 flush 一轮
			s.syncRound(context.Background())
			return
		}
	}
}

// Stop 停止同步循环并等待最后一轮 flush 完成。
func (s *Syncer) Stop() {
	if !s.started {
		return
	}
	close(s.stopCh)
	<-s.doneCh
}

func (s *Syncer) ensureSchema(ctx context.Context) error {
	if s.schemaReady {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, createTableDDL); err != nil {
		return fmt.Errorf("ensure schema: %w", err)
	}
	s.schemaReady = true
	return nil
}

// syncRound 消费脏集并 upsert;失败批次回补脏集,下轮重试(幂等)。
func (s *Syncer) syncRound(ctx context.Context) {
	if err := s.ensureSchema(ctx); err != nil {
		util.Logger().Error("rank sync: schema not ready", zap.Error(err))
		return
	}
	total := 0
	for {
		ids, err := s.storage.PopDirty(ctx, popBatchSize)
		if err != nil {
			util.Logger().Error("rank sync: pop dirty failed", zap.Error(err))
			return
		}
		if len(ids) == 0 {
			break
		}
		if err := s.upsertBatch(ctx, ids); err != nil {
			util.Logger().Error("rank sync: upsert failed, batch re-queued",
				zap.Error(err), zap.Int("batch", len(ids)))
			if markErr := s.storage.MarkDirty(ctx, ids); markErr != nil {
				util.Logger().Error("rank sync: re-queue failed", zap.Error(markErr))
			}
			return
		}
		total += len(ids)
	}
	if total > 0 {
		util.Logger().Info("rank sync: completed", zap.Int("players", total))
	}
}

func (s *Syncer) upsertBatch(ctx context.Context, ids []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, upsertSQL)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()
	for _, id := range ids {
		ub, ok, err := s.storage.GetUserBest(ctx, id)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if _, err := stmt.ExecContext(ctx,
			ub.UserId, ub.UserName, ub.Score.Stage, ub.Score.StageCultivation.String(), ub.Score.TotalCultivation.String(),
			repository_impl.ScoreKey(ub.Score), ub.AchievedAt, ub.ClientIP,
			ub.Province, ub.City, ub.District); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RebuildFromDB 全量读 MySQL 重放进 Redis;重放走只升不降 Lua,幂等且不标脏。
func (s *Syncer) RebuildFromDB(ctx context.Context) (int, error) {
	if !s.Enabled() {
		return 0, fmt.Errorf("rebuild requested but MYSQL_DSN is not set")
	}
	if err := s.ensureSchema(ctx); err != nil {
		return 0, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT user_id, user_name, stage, stage_cultivation, total_cultivation, achieved_at,
  client_ip, province, city, district FROM user_best_score`)
	if err != nil {
		return 0, fmt.Errorf("query user_best_score: %w", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		ub := &repository_impl.UserBest{}
		var last, total string
		if err := rows.Scan(&ub.UserId, &ub.UserName, &ub.Score.Stage, &last,
			&total, &ub.AchievedAt, &ub.ClientIP, &ub.Province, &ub.City, &ub.District); err != nil {
			return count, err
		}
		if ub.Score.StageCultivation, err = vo.ParseCultivation(last); err != nil {
			return count, fmt.Errorf("user %s stage_cultivation: %w", ub.UserId, err)
		}
		if ub.Score.TotalCultivation, err = vo.ParseCultivation(total); err != nil {
			return count, fmt.Errorf("user %s total_cultivation: %w", ub.UserId, err)
		}
		if err := s.storage.RestoreBest(ctx, ub); err != nil {
			return count, err
		}
		count++
	}
	return count, rows.Err()
}
