package sync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"fc_server/internal/domain/person/entity"
	"fc_server/internal/domain/person/entity/vo"
	"fc_server/internal/domain/person/repository"
	"fc_server/internal/infrastructure/repository_impl"
	"fc_server/internal/util"

	"go.uber.org/zap"
)

// player_profile 使用 utf8mb4_bin:user_id/openid 区分大小写,昵称可含 emoji。
const createPlayerTableDDL = `
CREATE TABLE IF NOT EXISTS player_profile (
  user_id       VARCHAR(64)   NOT NULL,
  appid         VARCHAR(64)   NOT NULL,
  openid        VARCHAR(128)  NOT NULL,
  nickname      VARCHAR(64)   NOT NULL DEFAULT '',
  avatar_url    VARCHAR(2048) NOT NULL DEFAULT '',
  created_at    BIGINT        NOT NULL,
  last_login_at BIGINT        NOT NULL,
  updated_at    BIGINT        NOT NULL,
  revision      BIGINT        NOT NULL,
  synced_at     TIMESTAMP     NOT NULL DEFAULT CURRENT_TIMESTAMP
                              ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (user_id),
  UNIQUE KEY uk_appid_openid (appid, openid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`

// upsertPlayerSQL 只接受更高的 revision;revision 必须最后赋值,
// 前面各列的 IF 比较的仍是旧 revision。
const upsertPlayerSQL = `
INSERT INTO player_profile
  (user_id, appid, openid, nickname, avatar_url, created_at, last_login_at, updated_at, revision)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
  nickname      = IF(VALUES(revision) > revision, VALUES(nickname), nickname),
  avatar_url    = IF(VALUES(revision) > revision, VALUES(avatar_url), avatar_url),
  last_login_at = IF(VALUES(revision) > revision, VALUES(last_login_at), last_login_at),
  updated_at    = IF(VALUES(revision) > revision, VALUES(updated_at), updated_at),
  revision      = GREATEST(revision, VALUES(revision))`

const playerScanBatch = 128

// PlayerSyncer 把 Redis 中待备份的玩家资料按版本增量写入 MySQL。
// 与排行榜同步器不同,它不先弹出脏标记:提交成功后按 revision 比较再清除,
// 因此数据库失败、进程崩溃或并发修改都不会丢失待同步更新。
type PlayerSyncer struct {
	db          *sql.DB // nil 表示停用
	storage     *repository_impl.PlayerRedisStorage
	interval    time.Duration
	schemaReady bool
	started     bool
	stopCh      chan struct{}
	doneCh      chan struct{}
}

func NewPlayerSyncer(db *sql.DB, storage *repository_impl.PlayerRedisStorage, interval time.Duration) *PlayerSyncer {
	return &PlayerSyncer{
		db:       db,
		storage:  storage,
		interval: interval,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
}

func (s *PlayerSyncer) Enabled() bool {
	return s.db != nil
}

func (s *PlayerSyncer) Start() {
	if !s.Enabled() {
		util.Logger().Warn("player profile backup disabled: MYSQL_DSN not set; profiles live only in redis")
		return
	}
	s.started = true
	go s.run()
}

func (s *PlayerSyncer) run() {
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

// Stop 停止同步循环并等待最后一轮 flush 完成。
func (s *PlayerSyncer) Stop() {
	if !s.started {
		return
	}
	close(s.stopCh)
	<-s.doneCh
}

func (s *PlayerSyncer) ensureSchema(ctx context.Context) error {
	if s.schemaReady {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, createPlayerTableDDL); err != nil {
		return fmt.Errorf("ensure player schema: %w", err)
	}
	s.schemaReady = true
	return nil
}

// syncRound 遍历一遍待备份集合;任一批失败即结束本轮,未确认的记录留待下轮。
func (s *PlayerSyncer) syncRound(ctx context.Context) {
	if err := s.ensureSchema(ctx); err != nil {
		util.Logger().Error("player sync: schema not ready", zap.Error(err))
		return
	}
	total := 0
	var cursor uint64
	for {
		entries, next, err := s.storage.ScanDirty(ctx, cursor, playerScanBatch)
		if err != nil {
			util.Logger().Error("player sync: scan dirty failed", zap.Error(err))
			return
		}
		if len(entries) > 0 {
			n, err := s.syncBatch(ctx, entries)
			if err != nil {
				util.Logger().Error("player sync: batch failed, will retry", zap.Error(err), zap.Int("batch", len(entries)))
				return
			}
			total += n
		}
		if next == 0 {
			break
		}
		cursor = next
	}
	if total > 0 {
		util.Logger().Info("player sync: completed", zap.Int("players", total))
	}
}

func (s *PlayerSyncer) syncBatch(ctx context.Context, entries []repository_impl.DirtyPlayer) (int, error) {
	snapshots := make([]*entity.Person, 0, len(entries))
	var orphans []repository_impl.DirtyPlayer
	for _, e := range entries {
		p, err := s.storage.Get(ctx, e.UserID)
		if errors.Is(err, repository.ErrPlayerNotFound) {
			orphans = append(orphans, e)
			continue
		}
		if err != nil {
			return 0, err
		}
		snapshots = append(snapshots, p)
	}

	if len(snapshots) > 0 {
		if err := s.upsertPlayers(ctx, snapshots); err != nil {
			return 0, err
		}
	}
	for _, p := range snapshots {
		if err := s.storage.AckDirty(ctx, p.ID, p.Revision); err != nil {
			return 0, err
		}
	}
	for _, o := range orphans {
		if err := s.storage.AckDirty(ctx, o.UserID, o.Revision); err != nil {
			return 0, err
		}
	}
	return len(snapshots), nil
}

func (s *PlayerSyncer) upsertPlayers(ctx context.Context, players []*entity.Person) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, upsertPlayerSQL)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()
	for _, p := range players {
		if _, err := stmt.ExecContext(ctx,
			p.ID, p.AppID, p.OpenID, p.Profile.Nickname, p.Profile.AvatarURL,
			p.CreatedTime, p.LastLoginTime, p.UpdatedTime, p.Revision); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RebuildFromDB 把 MySQL 中的玩家资料按 revision 恢复进 Redis(不恢复会话)。
// 必须在开放登录/资料写入前执行,否则空 Redis 会从 revision 1 重新计数。
func (s *PlayerSyncer) RebuildFromDB(ctx context.Context) (int, error) {
	if !s.Enabled() {
		return 0, fmt.Errorf("player rebuild requested but MYSQL_DSN is not set")
	}
	if err := s.ensureSchema(ctx); err != nil {
		return 0, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT user_id, appid, openid, nickname, avatar_url,
  created_at, last_login_at, updated_at, revision FROM player_profile`)
	if err != nil {
		return 0, fmt.Errorf("query player_profile: %w", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		p := &entity.Person{}
		var profile vo.Profile
		if err := rows.Scan(&p.ID, &p.AppID, &p.OpenID, &profile.Nickname, &profile.AvatarURL,
			&p.CreatedTime, &p.LastLoginTime, &p.UpdatedTime, &p.Revision); err != nil {
			return count, err
		}
		p.Profile = profile
		restored, err := s.storage.Restore(ctx, p)
		if err != nil {
			return count, err
		}
		if restored {
			count++
		}
	}
	return count, rows.Err()
}
