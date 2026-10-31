package dto

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"
)

// maxGameRecordDepth 嵌套层数上限,低于 MySQL JSON 的 100 层限制。
const maxGameRecordDepth = 64

// 游戏记录 POST /upload_game_record,需要 `Authorization: Bearer <token>`。
// 请求体本身就是一条记录:任意 JSON 对象,服务端原样保存、不解析业务字段。
//
//	{"stage":3,"result":"win","score":1200,"cards":["A","K"],"duration_ms":93000}
//
// 客户端要求:
//   - 请求体必须是 JSON 对象(以 { 开头),UTF-8 编码,整体不超过 16 KiB,嵌套不超过 64 层;
//     数组、字符串等非对象 JSON 返回 400。
//   - 不要在记录里放玩家身份:记录归属取自 token,body 里的 user_id 之类字段只会作为普通数据保存。
//   - 服务端会记录接收时间(created_at_ms);客户端如需事件发生时间,请自行放进记录字段。
//   - 成功(200)只表示已进入待写入队列,MySQL 是异步批量写入(默认 60 秒一轮)。
//   - 每次请求都生成新记录,重试会产生重复记录;只在网络失败等未收到响应时重试。
//   - 503 表示服务端积压已满或存储不可用,稍后再试,不要高频重试。

// GameRecordResponse 返回服务端分配的记录 ID 与接收时间(Unix 毫秒)。
type GameRecordResponse struct {
	RecordId    string `json:"record_id"`
	CreatedAtMs int64  `json:"created_at_ms"`
}

// NormalizeGameRecord 校验请求体为合法的 JSON 对象并压缩为紧凑格式。
func NormalizeGameRecord(body []byte) ([]byte, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, errors.New("request body is required")
	}
	data, err := normalizeJSONObject(body)
	if err != nil {
		return nil, fmt.Errorf("body %s", err.Error())
	}
	return data, nil
}

// normalizeJSONObject 校验为 UTF-8 的 JSON 对象、嵌套不超过 maxGameRecordDepth 层,并压缩为紧凑格式。
// 返回的错误信息以 "must ..." 开头,由调用方补上字段名。
func normalizeJSONObject(raw []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(raw)
	if !utf8.Valid(trimmed) {
		return nil, errors.New("must be valid UTF-8")
	}
	if len(trimmed) == 0 || !json.Valid(trimmed) {
		return nil, errors.New("must be valid JSON")
	}
	if trimmed[0] != '{' {
		return nil, errors.New("must be a JSON object")
	}
	if jsonDepth(trimmed) > maxGameRecordDepth {
		return nil, fmt.Errorf("must not nest more than %d levels", maxGameRecordDepth)
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, trimmed); err != nil {
		return nil, errors.New("must be valid JSON")
	}
	return buf.Bytes(), nil
}

// jsonDepth 已知合法 JSON 的最大嵌套层数。
func jsonDepth(data []byte) int {
	depth, maxDepth := 0, 0
	inString, escaped := false, false
	for _, b := range data {
		switch {
		case inString:
			switch {
			case escaped:
				escaped = false
			case b == '\\':
				escaped = true
			case b == '"':
				inString = false
			}
		case b == '"':
			inString = true
		case b == '{' || b == '[':
			depth++
			if depth > maxDepth {
				maxDepth = depth
			}
		case b == '}' || b == ']':
			depth--
		}
	}
	return maxDepth
}
