package repository

import (
	"context"
	"errors"

	"fc_server/internal/domain/record/entity"
)

// ErrQueueFull 待持久化的记录积压已达上限(通常是 MySQL 长时间不可用或未配置)。
var ErrQueueFull = errors.New("game record queue full")

// GameRecordQueue 在线链路只把记录追加进队列;由后台任务批量写入持久存储。
type GameRecordQueue interface {
	Append(ctx context.Context, record *entity.GameRecord) error
}
