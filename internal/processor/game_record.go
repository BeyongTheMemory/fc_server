package processor

import (
	"context"
	"fmt"

	"fc_server/internal/domain/record/application/impl"
	"fc_server/internal/processor/dto"
)

// UploadGameRecord 为已认证玩家追加一条游戏记录;body 是原始请求体(JSON 对象)。
func UploadGameRecord(ctx context.Context, userID, clientIP string, body []byte) (*dto.GameRecordResponse, error) {
	data, err := dto.NormalizeGameRecord(body)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidParam, err.Error())
	}
	record, err := impl.GetGameRecordApplicationImpl().Append(ctx, userID, data, clientIP)
	if err != nil {
		return nil, err
	}
	return &dto.GameRecordResponse{RecordId: record.ID, CreatedAtMs: record.CreatedAtMs}, nil
}
