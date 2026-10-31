package vo

import (
	"strings"
	"testing"
)

func TestParseCultivation(t *testing.T) {
	ok := map[string]string{
		"0":                     "0",
		"000":                   "0",
		"007":                   "7",
		"123456":                "123456",
		"931009731811524000000": "931009731811524000000",
		// JavaScript String(x) 对 ≥1e21 的整数输出科学计数法
		"1e+21":                   "1" + strings.Repeat("0", 21),
		"9.31009731811524e+23":    "931009731811524" + strings.Repeat("0", 9),
		"1.5E3":                   "1500",
		"2e5":                     "200000",
		"1.7976931348623157e+308": "17976931348623157" + strings.Repeat("0", 292),
	}
	for in, want := range ok {
		got, err := ParseCultivation(in)
		if err != nil || string(got) != want {
			t.Errorf("ParseCultivation(%q) = (%q, %v), want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{
		"", "-1", "1.5", "12a", " 12", "1e", "1.23e1", "+5", "0x10", "1e-3",
		strings.Repeat("9", MaxCultivationDigits+1), "1e320", "1e999999999999",
	} {
		if got, err := ParseCultivation(bad); err == nil {
			t.Errorf("ParseCultivation(%q) = %q, want error", bad, got)
		}
	}
	if _, err := ParseCultivation(strings.Repeat("9", MaxCultivationDigits)); err != nil {
		t.Errorf("max digits should be accepted: %v", err)
	}
}

func TestCultivationCompare(t *testing.T) {
	tests := []struct {
		a, b Cultivation
		want int
	}{
		{"9", "10", -1},
		{"100", "99", 1},
		{"12345", "12345", 0},
		{"12346", "12345", 1},
		{"", "0", 0},
		{Cultivation("1" + strings.Repeat("0", 300)), Cultivation("9" + strings.Repeat("9", 299)), 1},
	}
	for _, tt := range tests {
		if got := tt.a.Compare(tt.b); got != tt.want {
			t.Errorf("%q vs %q = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestScoreCompareOrder(t *testing.T) {
	s := func(stage int64, last, total string) Score {
		return Score{Stage: stage, StageCultivation: Cultivation(last), TotalCultivation: Cultivation(total)}
	}
	if s(10, "0", "0").Compare(s(9, "9"+strings.Repeat("9", 100), "1")) != 1 {
		t.Error("higher stage must win regardless of cultivation size")
	}
	if s(9, "1000", "1").Compare(s(9, "999", strings.Repeat("9", 50))) != 1 {
		t.Error("last-stage cultivation decides before total")
	}
	if s(9, "5", "100").Compare(s(9, "5", "99")) != 1 || s(9, "5", "99").Compare(s(9, "5", "99")) != 0 {
		t.Error("total decides last")
	}
	if !(Score{}).IsZero() || s(1, "0", "0").IsZero() {
		t.Error("IsZero must mean no stage")
	}
}
