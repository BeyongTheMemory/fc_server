package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env.local")
	content := `# 注释
PORT=8081
export WECHAT_APP_ID=wx_from_file
  REDIS_PASSWORD=
MYSQL_PASSWORD="p@ss word#1"
QUOTED_SINGLE='a=b'
ALREADY_SET=from_file
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"PORT", "WECHAT_APP_ID", "REDIS_PASSWORD", "MYSQL_PASSWORD", "QUOTED_SINGLE"} {
		t.Setenv(k, "") // 登记清理
		os.Unsetenv(k)
	}
	t.Setenv("ALREADY_SET", "from_env")

	if err := LoadDotEnv(path); err != nil {
		t.Fatalf("LoadDotEnv: %v", err)
	}
	want := map[string]string{
		"PORT":           "8081",
		"WECHAT_APP_ID":  "wx_from_file",
		"MYSQL_PASSWORD": "p@ss word#1",
		"QUOTED_SINGLE":  "a=b",
		"ALREADY_SET":    "from_env", // 真实环境变量优先
	}
	for k, v := range want {
		if got := os.Getenv(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if v, ok := os.LookupEnv("REDIS_PASSWORD"); !ok || v != "" {
		t.Errorf("empty value should be set as empty string, got %q (set=%v)", v, ok)
	}
}

func TestLoadDotEnvMissingFileIsNoop(t *testing.T) {
	if err := LoadDotEnv(filepath.Join(t.TempDir(), "absent")); err != nil {
		t.Errorf("missing file err = %v", err)
	}
}

func TestLoadDotEnvRejectsMalformedLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env.local")
	if err := os.WriteFile(path, []byte("GOOD=1\nthis is not valid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOOD", "")
	os.Unsetenv("GOOD")
	if err := LoadDotEnv(path); err == nil {
		t.Error("malformed line should be reported")
	}
}
