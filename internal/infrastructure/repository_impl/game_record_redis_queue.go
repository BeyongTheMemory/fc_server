package repository_impl

import (
	"context"
	"encoding/json"
	"fmt"

	"fc_server/internal/domain/record/entity"
	"fc_server/internal/domain/record/repository"

	"github.com/redis/go-redis/v9"
)

// Redis key 方案(游戏记录):
//
//	待持久化队列: game_record:queue        LIST,RPUSH 追加,消费者从头部读取
//	消费锁:       game_record:flush_lock   同一时刻只允许一个实例消费队列
const (
	keyGameRecordQueue     = "game_record:queue"
	keyGameRecordFlushLock = "game_record:flush_lock"
)

var _ repository.GameRecordQueue = (*GameRecordRedisQueue)(nil)

type queuedGameRecord struct {
	ID          string          `json:"id"`
	UserID      string          `json:"user_id"`
	CreatedAtMs int64           `json:"created_at_ms"`
	ClientIP    string          `json:"client_ip,omitempty"`
	Data        json.RawMessage `json:"data"`
}

type GameRecordRedisQueue struct {
	lockedListQueue
}

func NewGameRecordRedisQueue(client *redis.Client, maxLen int) *GameRecordRedisQueue {
	return &GameRecordRedisQueue{lockedListQueue{
		client: client, key: keyGameRecordQueue, lockKey: keyGameRecordFlushLock, maxLen: maxLen, name: "game record",
	}}
}

func (q *GameRecordRedisQueue) Append(ctx context.Context, record *entity.GameRecord) error {
	payload, err := json.Marshal(queuedGameRecord{
		ID:          record.ID,
		UserID:      record.UserID,
		CreatedAtMs: record.CreatedAtMs,
		ClientIP:    record.ClientIP,
		Data:        json.RawMessage(record.Data),
	})
	if err != nil {
		return fmt.Errorf("encode game record: %w", err)
	}
	ok, err := q.appendCapped(ctx, string(payload))
	if err != nil {
		return err
	}
	if !ok {
		return repository.ErrQueueFull
	}
	return nil
}

// Peek 读取队头至多 n 条(不移除)。返回解码成功的记录与实际读取的元素数;
// 无法解码的元素(不应出现)计入数量以便随批次一起移除,并通过 malformed 报告。
func (q *GameRecordRedisQueue) Peek(ctx context.Context, n int64) (records []*entity.GameRecord, count int, malformed int, err error) {
	raw, err := q.peekRaw(ctx, n)
	if err != nil {
		return nil, 0, 0, err
	}
	records = make([]*entity.GameRecord, 0, len(raw))
	for _, item := range raw {
		var r queuedGameRecord
		if err := json.Unmarshal([]byte(item), &r); err != nil || r.ID == "" || r.UserID == "" {
			malformed++
			continue
		}
		records = append(records, &entity.GameRecord{ID: r.ID, UserID: r.UserID, Data: r.Data, CreatedAtMs: r.CreatedAtMs, ClientIP: r.ClientIP})
	}
	return records, len(raw), malformed, nil
}
