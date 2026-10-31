package repository_impl

import (
	"sort"
	"strings"
	"testing"
	"time"

	"fc_server/internal/domain/rank/entity/vo"
)

func TestKeyBuilding(t *testing.T) {
	if got := globalKeyIn(baseAll); got != "lb:global" {
		t.Errorf("global key = %q", got)
	}
	if got := globalKeyIn(weekBase("2026-W36")); got != "lb:w:2026-W36:global" {
		t.Errorf("week global key = %q", got)
	}
	if got := provKeyIn(baseAll, "浙江省"); got != "lb:prov:浙江省" {
		t.Errorf("provKey = %q", got)
	}
	if got := cityKeyIn(baseAll, "浙江省", "杭州市"); got != "lb:city:浙江省|杭州市" {
		t.Errorf("cityKey = %q", got)
	}
	if got := distKeyIn(monthBase("2026-08"), "浙江省", "杭州市", "西湖区"); got != "lb:m:2026-08:dist:浙江省|杭州市|西湖区" {
		t.Errorf("distKey = %q", got)
	}
	if userKey("u_1") != "lb:user:u_1" || wUserKey("u_1") != "lb:wuser:u_1" || mUserKey("u_1") != "lb:muser:u_1" {
		t.Error("meta keys changed")
	}
}

func TestKeyBuildingIncompletePath(t *testing.T) {
	if got := cityKeyIn(baseAll, "", "杭州市"); got != "" {
		t.Errorf("cityKey without province = %q, want empty", got)
	}
	if got := distKeyIn(baseAll, "浙江省", "", "西湖区"); got != "" {
		t.Errorf("distKey without city = %q, want empty", got)
	}
	if got := provKeyIn(baseAll, ""); got != "" {
		t.Errorf("provKey empty = %q, want empty", got)
	}
}

func TestSameNameDistrictInDifferentCitiesGetsDifferentKeys(t *testing.T) {
	if distKeyIn(baseAll, "北京市", "北京市", "朝阳区") == distKeyIn(baseAll, "吉林省", "长春市", "朝阳区") {
		t.Error("same-name districts must not share a key")
	}
}

func TestMemberEncodingRoundTrip(t *testing.T) {
	for _, s := range []vo.Score{
		{Stage: 12, StageCultivation: "0", TotalCultivation: "3400"},
		{Stage: MaxEncodableStage, StageCultivation: vo.Cultivation("9" + strings.Repeat("9", vo.MaxCultivationDigits-1)), TotalCultivation: "1"},
	} {
		m := encodeMember(s, 1_793_000_000, "wx_abc-_XYZ")
		got, at, id, ok := decodeMember(m)
		if !ok || got != s || at != 1_793_000_000 || id != "wx_abc-_XYZ" {
			t.Fatalf("decode(%q) = (%+v, %d, %q, %v)", m, got, at, id, ok)
		}
	}
	if got := scoreKey(vo.Score{Stage: 12, StageCultivation: "3400", TotalCultivation: "98000"}); got != "0000000012"+"0043400"+"00598000" {
		t.Errorf("scoreKey = %q", got)
	}
	s := vo.Score{Stage: 1, StageCultivation: "5", TotalCultivation: "5"}
	for _, bad := range []string{"", "u_1", "000000001x:u", encodeMember(s, 1, ""), "0000000001" + "009" + "12:u"} {
		if _, _, _, ok := decodeMember(bad); ok {
			t.Errorf("decode(%q) should fail", bad)
		}
	}
}

// Redis 对同分成员按字节序排序;降序字节序必须与排名规则一致。
func TestMemberByteOrderMatchesRankingRule(t *testing.T) {
	type entry struct {
		id    string
		score vo.Score
		at    int64
	}
	// 期望排名顺序
	want := []entry{
		{"stage100", vo.Score{Stage: 100, StageCultivation: "0", TotalCultivation: "0"}, 50},
		{"stage9_last_31digits", vo.Score{Stage: 9, StageCultivation: vo.Cultivation("1" + strings.Repeat("0", 30)), TotalCultivation: "1"}, 50},
		{"stage9_last900", vo.Score{Stage: 9, StageCultivation: "900", TotalCultivation: "1"}, 50},
		{"stage9_last80_total_long", vo.Score{Stage: 9, StageCultivation: "80", TotalCultivation: "100000"}, 50},
		{"stage9_last80_total5", vo.Score{Stage: 9, StageCultivation: "80", TotalCultivation: "5"}, 50},
		{"stage9_last80_total4_early", vo.Score{Stage: 9, StageCultivation: "80", TotalCultivation: "4"}, 10},
		{"stage9_last80_total4_late", vo.Score{Stage: 9, StageCultivation: "80", TotalCultivation: "4"}, 20},
		{"stage1", vo.Score{Stage: 1, StageCultivation: "1000000", TotalCultivation: "9000000"}, 1},
	}
	members := make([]string, len(want))
	for i, e := range want {
		members[len(want)-1-i] = encodeMember(e.score, e.at, e.id)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(members)))
	for i, m := range members {
		if _, _, id, _ := decodeMember(m); id != want[i].id {
			t.Errorf("position %d = %s, want %s", i, id, want[i].id)
		}
	}
}

func TestWindowIds(t *testing.T) {
	tests := []struct {
		name string
		time time.Time
		wWin string
		mWin string
	}{
		{"regular day", time.Date(2026, 8, 15, 12, 0, 0, 0, cnZone), "2026-W33", "2026-08"},
		{"iso year boundary", time.Date(2025, 12, 29, 12, 0, 0, 0, cnZone), "2026-W01", "2025-12"},
		{"jan 1st", time.Date(2026, 1, 1, 12, 0, 0, 0, cnZone), "2026-W01", "2026-01"},
		{"sunday night", time.Date(2026, 9, 6, 23, 59, 0, 0, cnZone), "2026-W36", "2026-09"},
		{"monday midnight rolls week", time.Date(2026, 9, 7, 0, 0, 1, 0, cnZone), "2026-W37", "2026-09"},
		{"utc converted to cn zone", time.Date(2026, 9, 6, 16, 30, 0, 0, time.UTC), "2026-W37", "2026-09"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := weekWindowId(tt.time); got != tt.wWin {
				t.Errorf("weekWindowId = %q, want %q", got, tt.wWin)
			}
			if got := monthWindowId(tt.time); got != tt.mWin {
				t.Errorf("monthWindowId = %q, want %q", got, tt.mWin)
			}
		})
	}
}
