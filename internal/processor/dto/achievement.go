package dto

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"
)

const (
	maxAchievementIDLen = 64
	maxDetailBytes      = 4 << 10
	// minUnlockedAtMs 2020-01-01,早于此的解锁时间视为客户端时钟错误
	minUnlockedAtMs = 1_577_836_800_000
	// maxClockSkew 允许客户端时间领先服务端的上限
	maxClockSkew = 24 * time.Hour
)

var achievementIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]+$`)

// UploadAchievementRequest POST /upload_achievement,需要 `Authorization: Bearer <token>`。
//
//	{"achievement_id":"first_ascension","unlocked_at_ms":1793380000000,"detail":{"stage":19,"deck":"wood"}}
//
// 字段:
//   - achievement_id(必填):成就标识,1~64 个字符,只能包含字母、数字与 _ . : -。
//     由客户端统一定义,发布后不要改名;分档成就用不同 ID(如 daluo_10、daluo_20)。
//   - unlocked_at_ms(可选):客户端解锁时间,Unix 毫秒;省略或 0 时用服务端接收时间。
//     早于 2020 年或领先服务端超过 24 小时会被拒绝(400)。
//   - detail(可选):附加信息,必须是 JSON 对象,序列化后 ≤4 KiB、嵌套 ≤64 层;服务端原样保存不解析。
//
// 语义:每位玩家的每项成就只记录一次(首次解锁)。重复上传是安全的:返回 newly_unlocked=false
// 和首次解锁时的时间,不会覆盖首次记录,也不会报错。
//
// 客户端要求:
//   - 成就达成时立即上报;失败(网络、5xx、401 重登后)可重试,重复上报不会产生重复数据。
//     建议本地记录"待上报成就",下次启动补报,直到收到 200。
//   - 503 表示服务端暂时无法保存,稍后重试,不要高频重试。
//   - 是否展示"新解锁"提示以本地判断为准;newly_unlocked 仅表示服务端此前是否已有记录
//     (换设备后首次补报也会是 true)。
type UploadAchievementRequest struct {
	AchievementId string          `json:"achievement_id"`
	UnlockedAtMs  int64           `json:"unlocked_at_ms"`
	Detail        json.RawMessage `json:"detail"`

	detail []byte
}

// Validate 校验字段;now 用于判断解锁时间是否合理。成功后 NormalizedDetail 返回压缩后的 detail。
func (r *UploadAchievementRequest) Validate(now time.Time) error {
	if r == nil {
		return errors.New("request body is required")
	}
	if r.AchievementId == "" || len(r.AchievementId) > maxAchievementIDLen || !achievementIDPattern.MatchString(r.AchievementId) {
		return fmt.Errorf("achievement_id must be 1-%d characters of letters, digits, _ . : -", maxAchievementIDLen)
	}
	if r.UnlockedAtMs != 0 && (r.UnlockedAtMs < minUnlockedAtMs || r.UnlockedAtMs > now.Add(maxClockSkew).UnixMilli()) {
		return errors.New("unlocked_at_ms is out of range (unix milliseconds, not before 2020 and not in the future)")
	}
	r.detail = nil
	if raw := bytes.TrimSpace(r.Detail); len(raw) > 0 && string(raw) != "null" {
		d, err := normalizeJSONObject(raw)
		if err != nil {
			return fmt.Errorf("detail %s", err.Error())
		}
		if len(d) > maxDetailBytes {
			return fmt.Errorf("detail exceeds %d bytes", maxDetailBytes)
		}
		r.detail = d
	}
	return nil
}

func (r *UploadAchievementRequest) NormalizedDetail() []byte {
	return r.detail
}

// UploadAchievementResponse 生效的成就记录:newly_unlocked 表示本次是否为服务端首次记录,
// unlocked_at_ms 为首次解锁时间。
type UploadAchievementResponse struct {
	AchievementId string `json:"achievement_id"`
	NewlyUnlocked bool   `json:"newly_unlocked"`
	UnlockedAtMs  int64  `json:"unlocked_at_ms"`
}
