package impl

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"fc_server/internal/domain/record/application"
	"fc_server/internal/domain/record/entity"
	"fc_server/internal/domain/record/repository"
)

var (
	gameRecordApplicationImpl *GameRecordApplicationImpl
	_                         application.GameRecordApplication = (*GameRecordApplicationImpl)(nil)
)

func Init(queue repository.GameRecordQueue) {
	gameRecordApplicationImpl = New(queue)
}

func GetGameRecordApplicationImpl() application.GameRecordApplication {
	if gameRecordApplicationImpl == nil {
		panic("gameRecordApplicationImpl is not initialized")
	}
	return gameRecordApplicationImpl
}

type GameRecordApplicationImpl struct {
	queue repository.GameRecordQueue
	now   func() time.Time
}

func New(queue repository.GameRecordQueue) *GameRecordApplicationImpl {
	return &GameRecordApplicationImpl{queue: queue, now: time.Now}
}

func (a *GameRecordApplicationImpl) Append(ctx context.Context, userID string, data []byte, clientIP string) (*entity.GameRecord, error) {
	id, err := newRecordID()
	if err != nil {
		return nil, fmt.Errorf("generate record id: %w", err)
	}
	record := &entity.GameRecord{
		ID:          id,
		UserID:      userID,
		Data:        data,
		CreatedAtMs: a.now().UnixMilli(),
		ClientIP:    clientIP,
	}
	if err := a.queue.Append(ctx, record); err != nil {
		return nil, fmt.Errorf("%w: %w", application.ErrUnavailable, err)
	}
	return record, nil
}

func newRecordID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
