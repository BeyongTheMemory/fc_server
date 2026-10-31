package dto

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"fc_server/internal/domain/rank/entity/vo"
)

// maxJSONSafeInteger JavaScript Number.MAX_SAFE_INTEGER;超过它的修为必须以字符串传输。
const maxJSONSafeInteger = 9_007_199_254_740_991

// UploadScoreRequest POST /upload_score,需要 `Authorization: Bearer <token>`。
//
//	{"run_id":"<POST /runs/start 返回的 run_id>",
//	 "location":{"province":"浙江省","city":"杭州市","district":"西湖区"},
//	 "stage":38,"stage_cultivation":"931009731811524","total_cultivation":"1.2345678901234567e+21",
//	 "profile":{"nickname":"阿甘","avatar_url":"https://thirdwx.qlogo.cn/.../132"}}
//
// 排名规则:先比到达的关卡数(stage),相同再比最后一关修为(stage_cultivation),
// 再相同比整局修为总数(total_cultivation),仍相同则先达成者在前。
// 每个榜只保留玩家的最好一局:新成绩按上述规则更好才会替换(总榜、本月、本周各自比较)。
//
// 修为数值协议(大罗无尽模式的修为可超过 JavaScript 安全整数与 int64):
//   - stage_cultivation、total_cultivation 以十进制**字符串**传输,值为非负整数,不限大小
//     (最多 320 位,覆盖 Number.MAX_VALUE)。服务端精确比较,不做浮点换算。
//   - 前端推荐写法:`String(Math.floor(x))`。x < 1e21 时得到纯数字;x ≥ 1e21 时 JavaScript 输出
//     科学计数法(如 "9.31009731811524e+23"),服务端按其表示的整数值规范化,两种形式都接受。
//     Number 本身只有约 17 位有效数字,超过 2^53 的修为在前端已是近似值;服务端如实保存其整数值。
//   - 兼容:修为也可以是 JSON 整数,但仅限 ≤ 9007199254740991(Number.MAX_SAFE_INTEGER)。
//     更大的值必须用字符串,否则 400,避免 JSON 数字在传输中被静默舍入。
//   - 不接受负数、小数、NaN/Infinity;x 不是有限数时不要上报。
//   - 响应中的修为一律是规范化后的纯数字字符串(无前导零)。展示时可 `Number(s)` 换算为近似值
//     再格式化为万/亿;比较两个成绩请先比字符串长度再比字典序,不要转成 Number 比较。
//
// 对局票(防刷分):
//   - run_id 必填,来自本局开局时的 POST /runs/start;只能由领票的玩家使用,过期或不存在返回 400。
//   - 每局只能提交一份成绩:同一份成绩重试返回 200(updated=false,best 为当前最好成绩),
//     同一局提交不同成绩返回 409。
//   - 梦蝶局(玩家自定义种子)不计排名,没有对局票,不要调用本接口。
//   - 服务端可配置每关最短耗时(MIN_SECONDS_PER_STAGE),快于此返回 400。
//
// 客户端要求:
//   - 每局结束(陨落)时上报一次:stage = 本局到达的关卡数(≥1),stage_cultivation = 最后一关获得的修为,
//     total_cultivation = 整局获得的修为总数(含最后一关,必须 ≥ stage_cultivation)。不要上报账号累计修为。
//   - 玩家身份只取自 token,请求里不传 user_id;token 失效(401)时重新 wx.login 登录后重试一次。
//   - profile 可选,见 ProfileDto:玩家已授权头像昵称时带上,服务端先保存资料再记分;
//     玩家拒绝或尚未授权时省略,服务端沿用已保存的资料(从未授权则榜单显示“无名”与默认头像)。
//   - 任一字段不合法时整个请求返回 400,不记分、不改资料,修正后重试。
//   - 重复上传同一局是安全的(updated=false,不会产生新纪录)。
//   - location 规则见 LocationDto。
type UploadScoreRequest struct {
	RunId            string          `json:"run_id"`
	Location         *LocationDto    `json:"location"`
	Stage            int64           `json:"stage"`
	StageCultivation json.RawMessage `json:"stage_cultivation"`
	TotalCultivation json.RawMessage `json:"total_cultivation"`
	Profile          *ProfileDto     `json:"profile"`

	score vo.Score
}

// Validate 校验并解析成绩;成功后 ToScore 返回解析结果。
func (r *UploadScoreRequest) Validate(maxStage int64) error {
	if r == nil {
		return errors.New("request body is required")
	}
	if err := validateRunID(r.RunId); err != nil {
		return err
	}
	if err := r.Location.Validate(); err != nil {
		return err
	}
	if r.Stage < 1 || r.Stage > maxStage {
		return fmt.Errorf("stage must be between 1 and %d", maxStage)
	}
	last, err := parseCultivationField("stage_cultivation", r.StageCultivation)
	if err != nil {
		return err
	}
	total, err := parseCultivationField("total_cultivation", r.TotalCultivation)
	if err != nil {
		return err
	}
	if total.Compare(last) < 0 {
		return errors.New("total_cultivation must not be less than stage_cultivation")
	}
	r.score = vo.Score{Stage: r.Stage, StageCultivation: last, TotalCultivation: total}
	return nil
}

func (r *UploadScoreRequest) ToScore() vo.Score {
	return r.score
}

// parseCultivationField 接受十进制字符串(含 JS 科学计数法)或不超过安全整数的 JSON 整数。
func parseCultivationField(name string, raw json.RawMessage) (vo.Cultivation, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return "", fmt.Errorf("%s is required", name)
	}
	var s string
	if raw[0] == '"' {
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", fmt.Errorf("%s must be a decimal string", name)
		}
	} else {
		n, err := strconv.ParseUint(string(raw), 10, 64)
		if err != nil {
			return "", fmt.Errorf("%s must be a non-negative integer or a decimal string", name)
		}
		if n > maxJSONSafeInteger {
			return "", fmt.Errorf("%s exceeds %d, send it as a decimal string", name, uint64(maxJSONSafeInteger))
		}
		s = string(raw)
	}
	c, err := vo.ParseCultivation(s)
	if err != nil {
		return "", fmt.Errorf("%s %s", name, err.Error())
	}
	return c, nil
}

// UploadScoreResponse updated 表示本局刷新了历史最好成绩;best 为当前历史最好成绩。
type UploadScoreResponse struct {
	Updated bool      `json:"updated"`
	Best    *ScoreDto `json:"best"`
}

// ScoreDto 一局成绩。修为为规范化的十进制字符串(见 UploadScoreRequest 的数值协议)。
type ScoreDto struct {
	Stage            int64  `json:"stage"`
	StageCultivation string `json:"stage_cultivation"`
	TotalCultivation string `json:"total_cultivation"`
}

// NewScoreDto 零值成绩(未上榜)返回 nil,序列化为 null。
func NewScoreDto(s vo.Score) *ScoreDto {
	if s.IsZero() {
		return nil
	}
	return &ScoreDto{Stage: s.Stage, StageCultivation: s.StageCultivation.String(), TotalCultivation: s.TotalCultivation.String()}
}
