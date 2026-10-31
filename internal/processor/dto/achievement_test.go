package dto

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestUploadAchievementValidate(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)

	ok := &UploadAchievementRequest{AchievementId: "daluo_10", UnlockedAtMs: 1_799_999_000_000,
		Detail: json.RawMessage("{\n  \"stage\": 37,\n  \"note\": \"大罗🎴\"\n}")}
	if err := ok.Validate(now); err != nil {
		t.Fatalf("valid request: %v", err)
	}
	if string(ok.NormalizedDetail()) != `{"stage":37,"note":"大罗🎴"}` {
		t.Errorf("detail = %s", ok.NormalizedDetail())
	}
	for _, r := range []*UploadAchievementRequest{
		{AchievementId: "a"},
		{AchievementId: "ns:first-ascension.v2", Detail: json.RawMessage(`null`)},
		{AchievementId: strings.Repeat("x", 64), UnlockedAtMs: now.Add(23 * time.Hour).UnixMilli()},
	} {
		if err := r.Validate(now); err != nil || r.NormalizedDetail() != nil {
			t.Errorf("%+v: err = %v detail = %s", r, err, r.NormalizedDetail())
		}
	}

	bad := map[string]*UploadAchievementRequest{
		"missing id":       {},
		"id too long":      {AchievementId: strings.Repeat("x", 65)},
		"id with space":    {AchievementId: "first ascension"},
		"id with chinese":  {AchievementId: "飞升"},
		"seconds not ms":   {AchievementId: "a", UnlockedAtMs: 1_793_000_000},
		"far future":       {AchievementId: "a", UnlockedAtMs: now.Add(25 * time.Hour).UnixMilli()},
		"negative time":    {AchievementId: "a", UnlockedAtMs: -1},
		"detail array":     {AchievementId: "a", Detail: json.RawMessage(`[1,2]`)},
		"detail string":    {AchievementId: "a", Detail: json.RawMessage(`"x"`)},
		"detail too large": {AchievementId: "a", Detail: json.RawMessage(`{"pad":"` + strings.Repeat("a", 4100) + `"}`)},
		"detail too deep":  {AchievementId: "a", Detail: json.RawMessage(strings.Repeat(`{"a":`, 65) + `1` + strings.Repeat(`}`, 65))},
	}
	for name, r := range bad {
		if err := r.Validate(now); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	var nilReq *UploadAchievementRequest
	if err := nilReq.Validate(now); err == nil {
		t.Error("nil request should fail")
	}
}
