package sync

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"fc_server/internal/util"

	"github.com/go-sql-driver/mysql"
	"go.uber.org/zap"
)

const (
	queueBatch     = 200
	queueLockTTL   = 2 * time.Minute
	queueRoundTime = time.Minute // 必须小于锁 TTL,保证一轮内锁不过期
)

// 只因单条数据内容被 MySQL 拒绝的错误;遇到时丢弃该条并记录日志,避免一条坏数据堵住整个队列。
// 连接、锁等待等其它错误会让整批重试。
var poisonRecordErrors = map[uint16]bool{
	1366: true, // ER_TRUNCATED_WRONG_VALUE_FOR_FIELD(非法字符)
	1406: true, // ER_DATA_TOO_LONG
	3140: true, // ER_INVALID_JSON_TEXT
	3157: true, // ER_JSON_DOCUMENT_TOO_DEEP
}

// lockedQueue Redis 待持久化队列(见 repository_impl.lockedListQueue)。
type lockedQueue[T any] interface {
	AcquireFlushLock(ctx context.Context, token string, ttl time.Duration) (bool, error)
	ReleaseFlushLock(ctx context.Context, token string) error
	Peek(ctx context.Context, n int64) (items []T, count int, malformed int, err error)
	TrimIfLocked(ctx context.Context, token string, n int) (bool, error)
}

// drainQueue 一轮消费:持锁 → 读队头一批 → insert 写入 MySQL → 仍持锁时删除队头。
// 任何一步失败都保留队列,下轮重试;insert 必须对重放幂等(由唯一键去重)。
func drainQueue[T any](ctx context.Context, name string, q lockedQueue[T], insert func(context.Context, []T) (int, error)) {
	token, err := lockToken()
	if err != nil {
		util.Logger().Error(name+" sync: lock token", zap.Error(err))
		return
	}
	locked, err := q.AcquireFlushLock(ctx, token, queueLockTTL)
	if err != nil {
		util.Logger().Error(name+" sync: acquire lock failed", zap.Error(err))
		return
	}
	if !locked {
		return // 另一实例正在消费
	}
	defer func() {
		if err := q.ReleaseFlushLock(context.Background(), token); err != nil {
			util.Logger().Warn(name+" sync: release lock failed", zap.Error(err))
		}
	}()

	deadline := time.Now().Add(queueRoundTime)
	total, dropped := 0, 0
	for time.Now().Before(deadline) {
		items, count, malformed, err := q.Peek(ctx, queueBatch)
		if err != nil {
			util.Logger().Error(name+" sync: peek failed", zap.Error(err))
			break
		}
		if count == 0 {
			break
		}
		if malformed > 0 {
			util.Logger().Error(name+" sync: dropping undecodable queue items", zap.Int("count", malformed))
		}
		d, err := insert(ctx, items)
		if err != nil {
			util.Logger().Error(name+" sync: insert failed, will retry", zap.Error(err), zap.Int("batch", len(items)))
			break
		}
		trimmed, err := q.TrimIfLocked(ctx, token, count)
		if err != nil {
			util.Logger().Error(name+" sync: trim failed, batch will be replayed", zap.Error(err))
			break
		}
		if !trimmed {
			util.Logger().Warn(name + " sync: lock lost, batch will be replayed and deduplicated")
			break
		}
		total += len(items) - d
		dropped += d + malformed
	}
	if total > 0 || dropped > 0 {
		util.Logger().Info(name+" sync: completed", zap.Int("records", total), zap.Int("dropped", dropped))
	}
}

// insertRows 在一个事务中对每条数据执行 query,返回因内容非法被丢弃的条数。
// args 返回第 i 条的参数,logFields 返回丢弃时用于定位的日志字段。
func insertRows(ctx context.Context, db *sql.DB, name, query string, n int,
	args func(i int) []any, logFields func(i int) []zap.Field) (int, error) {
	if n == 0 {
		return 0, nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		return 0, err
	}
	defer func() { _ = stmt.Close() }()

	dropped := 0
	for i := 0; i < n; i++ {
		_, err := stmt.ExecContext(ctx, args(i)...)
		var myErr *mysql.MySQLError
		switch {
		case err == nil:
		case errors.As(err, &myErr) && poisonRecordErrors[myErr.Number]:
			dropped++
			util.Logger().Error(name+" sync: record rejected by mysql, dropped",
				append(logFields(i), zap.Uint16("mysql_errno", myErr.Number))...)
		default:
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return dropped, nil
}

func lockToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
