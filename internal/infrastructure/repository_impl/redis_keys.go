package repository_impl

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"fc_server/internal/domain/rank/entity/vo"
)

// Redis key 方案(排行榜):
//
//	全国榜(天榜): lb:global / lb:w:{YYYY-Www}:global / lb:m:{YYYY-MM}:global
//	地区榜(总):   lb:prov:{省} / lb:city:{省}|{市} / lb:dist:{省}|{市}|{区}
//	周榜:         lb:w:{YYYY-Www}:prov:{省} ...(ISO 周,周一起,Asia/Shanghai)
//	月榜:         lb:m:{YYYY-MM}:prov:{省} ...(自然月)
//	元数据:       lb:user:{id}(总)/ lb:wuser:{id}(周)/ lb:muser:{id}(月)
//	脏数据集:     lb:dirty(仅总榜同步 MySQL)
//
// 榜单 ZSET 中所有成员的 score 都是 0,排序完全由成员字符串决定(见 encodeMember):
// Redis 对同分成员按字节序排列,ZREVRANGE / ZREVRANK 因此按成绩降序。
// 这样三项成绩的取值不受 float64 精度限制,也不需要把它们压进一个数字。
//
// 市级 key 需要省+市齐全,区级需要省+市+区齐全;路径不全的层级不参与榜单。
// 分隔符 `|` 由 processor 层校验保证不出现在地区字段中。
// 窗口 key 每次写入时 EXPIRE,旧窗口停止写入后自动过期(FR-013)。
const (
	baseAll       = "lb:"
	keyPrefixUser = "lb:user:"
	keyDirtySet   = "lb:dirty"
	pathSep       = "|"

	weekTTLSeconds  = 14 * 24 * 3600
	monthTTLSeconds = 62 * 24 * 3600
)

// 成员编码:关卡(10 位)+ 最后一关修为 + 修为总数 + 反转的达成时间(10 位)+ ":" + user_id。
// 修为位数不定,编码为"3 位位数 + 数字":位数多的数值更大,位数相同时数字按字典序比较,
// 因此整个成员串按字节序比较即按排名规则比较,且各字段可以依次解析。
// 达成时间取 maxEncodedTime - t,越早越大,降序时同成绩先达成者在前。
const (
	stageWidth     = 10
	digitsWidth    = 3
	timeWidth      = 10
	maxEncodedTime = 9_999_999_999

	MaxEncodableStage = 9_999_999_999
)

// scoreKey 成绩部分的有序编码,同时用作 Lua 与 MySQL 中比较成绩的键。
func scoreKey(s vo.Score) string {
	return fmt.Sprintf("%0*d%s%s", stageWidth, s.Stage, encodeCultivation(s.StageCultivation), encodeCultivation(s.TotalCultivation))
}

func encodeCultivation(c vo.Cultivation) string {
	d := c.String()
	return fmt.Sprintf("%0*d%s", digitsWidth, len(d), d)
}

// ScoreKey 供 MySQL 备份使用的成绩比较列。
func ScoreKey(s vo.Score) string { return scoreKey(s) }

func encodeMember(s vo.Score, achievedAt int64, userID string) string {
	return fmt.Sprintf("%s%0*d:%s", scoreKey(s), timeWidth, maxEncodedTime-achievedAt, userID)
}

func decodeMember(m string) (score vo.Score, achievedAt int64, userID string, ok bool) {
	rest := m
	take := func(n int) (string, bool) {
		if len(rest) < n {
			return "", false
		}
		v := rest[:n]
		rest = rest[n:]
		return v, true
	}
	number := func(n int) (int64, bool) {
		v, ok := take(n)
		if !ok {
			return 0, false
		}
		x, err := strconv.ParseInt(v, 10, 64)
		return x, err == nil && x >= 0
	}
	cultivation := func() (vo.Cultivation, bool) {
		n, ok := number(digitsWidth)
		if !ok || n < 1 {
			return "", false
		}
		v, ok := take(int(n))
		if !ok || strings.Trim(v, "0123456789") != "" {
			return "", false
		}
		return vo.Cultivation(v), true
	}

	stage, ok1 := number(stageWidth)
	last, ok2 := cultivation()
	total, ok3 := cultivation()
	inv, ok4 := number(timeWidth)
	if !(ok1 && ok2 && ok3 && ok4) || len(rest) < 2 || rest[0] != ':' {
		return vo.Score{}, 0, "", false
	}
	return vo.Score{Stage: stage, StageCultivation: last, TotalCultivation: total}, maxEncodedTime - inv, rest[1:], true
}

var cnZone = loadCNZone()

func loadCNZone() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("CST", 8*3600)
	}
	return loc
}

// weekWindowId ISO 周期号,如 2026-W36(周一为一周起点)。
func weekWindowId(t time.Time) string {
	year, week := t.In(cnZone).ISOWeek()
	return fmt.Sprintf("%d-W%02d", year, week)
}

// monthWindowId 自然月期号,如 2026-08。
func monthWindowId(t time.Time) string {
	return t.In(cnZone).Format("2006-01")
}

func weekBase(win string) string  { return baseAll + "w:" + win + ":" }
func monthBase(win string) string { return baseAll + "m:" + win + ":" }

func globalKeyIn(base string) string { return base + "global" }

func provKeyIn(base, province string) string {
	if province == "" {
		return ""
	}
	return base + "prov:" + province
}

func cityKeyIn(base, province, city string) string {
	if province == "" || city == "" {
		return ""
	}
	return base + "city:" + province + pathSep + city
}

func distKeyIn(base, province, city, district string) string {
	if province == "" || city == "" || district == "" {
		return ""
	}
	return base + "dist:" + province + pathSep + city + pathSep + district
}

func userKey(userId string) string  { return keyPrefixUser + userId }
func wUserKey(userId string) string { return baseAll + "wuser:" + userId }
func mUserKey(userId string) string { return baseAll + "muser:" + userId }
