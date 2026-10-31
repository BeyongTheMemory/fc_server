package vo

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// MaxCultivationDigits 修为最多的十进制位数。覆盖 JavaScript Number.MAX_VALUE(309 位),
// 也受排行榜编码中 3 位长度前缀的约束(≤999)。
const MaxCultivationDigits = 320

// Cultivation 非负整数修为,任意大小,以规范十进制字符串保存:只含数字、无前导零,零为 "0"。
// 不使用 int64/float64:大罗无尽模式的修为会超过 int64 与 JavaScript 安全整数范围。
type Cultivation string

var sciPattern = regexp.MustCompile(`^([0-9]+)(?:\.([0-9]+))?[eE]\+?([0-9]+)$`)

// ParseCultivation 接受纯数字("123456")或 JavaScript Number 转字符串产生的科学计数法
// ("9.31e+23"),规范化为纯数字。值必须是整数:科学计数法的小数位不能多于指数。
func ParseCultivation(s string) (Cultivation, error) {
	if s == "" {
		return "", errors.New("is empty")
	}
	digits := s
	if m := sciPattern.FindStringSubmatch(s); m != nil {
		exp, err := strconv.Atoi(m[3])
		if err != nil || exp > MaxCultivationDigits {
			return "", fmt.Errorf("exceeds %d digits", MaxCultivationDigits)
		}
		frac := m[2]
		if len(frac) > exp {
			return "", errors.New("must be an integer")
		}
		digits = m[1] + frac + strings.Repeat("0", exp-len(frac))
	} else if strings.Trim(s, "0123456789") != "" {
		return "", errors.New("must be a non-negative integer in decimal")
	}
	digits = strings.TrimLeft(digits, "0")
	if digits == "" {
		digits = "0"
	}
	if len(digits) > MaxCultivationDigits {
		return "", fmt.Errorf("exceeds %d digits", MaxCultivationDigits)
	}
	return Cultivation(digits), nil
}

// CultivationFromInt 由非负 int64 构造(内部与测试使用)。
func CultivationFromInt(n int64) Cultivation {
	if n < 0 {
		n = 0
	}
	return Cultivation(strconv.FormatInt(n, 10))
}

// Compare 数值比较:位数多者大,位数相同按字典序。空值视为 0。
func (c Cultivation) Compare(o Cultivation) int {
	a, b := c.norm(), o.norm()
	switch {
	case len(a) != len(b):
		return cmpInt(len(a), len(b))
	case a == b:
		return 0
	case a > b:
		return 1
	default:
		return -1
	}
}

func (c Cultivation) String() string { return string(c.norm()) }

func (c Cultivation) norm() Cultivation {
	if c == "" {
		return "0"
	}
	return c
}

// Score 一局成绩。排名依次比较:到达的关卡数 → 最后一关修为 → 整局修为总数,
// 三者都相同时由达成时间决定(先达成者在前)。
type Score struct {
	Stage            int64       // 到达的关卡数
	StageCultivation Cultivation // 最后一关获得的修为
	TotalCultivation Cultivation // 整局获得的修为总数
}

// Compare 返回 1 / 0 / -1 表示 s 优于 / 等于 / 劣于 o。
func (s Score) Compare(o Score) int {
	if s.Stage != o.Stage {
		if s.Stage > o.Stage {
			return 1
		}
		return -1
	}
	if c := s.StageCultivation.Compare(o.StageCultivation); c != 0 {
		return c
	}
	return s.TotalCultivation.Compare(o.TotalCultivation)
}

// Fingerprint 唯一标识这份成绩(用于判断同一局的重复提交是否是同一成绩)。
func (s Score) Fingerprint() string {
	return fmt.Sprintf("%d/%s/%s", s.Stage, s.StageCultivation.String(), s.TotalCultivation.String())
}

// IsZero 没有成绩(未上榜)。有效成绩的关卡数至少为 1。
func (s Score) IsZero() bool {
	return s.Stage == 0
}

func cmpInt(a, b int) int {
	switch {
	case a > b:
		return 1
	case a < b:
		return -1
	default:
		return 0
	}
}
