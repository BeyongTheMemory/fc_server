package dto

import (
	"strings"
	"testing"
)

func TestNormalizeGameRecord(t *testing.T) {
	got, err := NormalizeGameRecord([]byte(" {\n  \"stage\": 3,\n  \"cards\": [\"A\", \"K\"],\n  \"note\": \"胜利 🎴\"\n} "))
	if err != nil {
		t.Fatalf("valid object: %v", err)
	}
	if string(got) != `{"stage":3,"cards":["A","K"],"note":"胜利 🎴"}` {
		t.Errorf("compacted = %s", got)
	}
	if got, err := NormalizeGameRecord([]byte(`{}`)); err != nil || string(got) != `{}` {
		t.Errorf("empty object = (%s, %v)", got, err)
	}

	deepOK := strings.Repeat(`{"a":`, 64) + `1` + strings.Repeat(`}`, 64)
	if _, err := NormalizeGameRecord([]byte(deepOK)); err != nil {
		t.Errorf("64 levels should pass: %v", err)
	}
	// 字符串里的括号不计入层数
	if _, err := NormalizeGameRecord([]byte(`{"s":"` + strings.Repeat("{[", 100) + `\"}"}`)); err != nil {
		t.Errorf("brackets inside strings must not count: %v", err)
	}

	for name, body := range map[string]string{
		"empty":        "",
		"blank":        "   ",
		"array":        `[1,2]`,
		"string":       `"hello"`,
		"number":       `42`,
		"null":         `null`,
		"invalid json": `{"a":`,
		"trailing":     `{"a":1} {"b":2}`,
		"invalid utf8": "{\"a\":\"\xff\"}",
		"too deep":     strings.Repeat(`{"a":`, 65) + `1` + strings.Repeat(`}`, 65),
	} {
		if _, err := NormalizeGameRecord([]byte(body)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
