package util

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestSetupLoggerSplitsByLevel(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	if err := SetupLogger(LogOptions{Dir: dir, RetentionDays: 30}); err != nil {
		t.Fatalf("SetupLogger: %v", err)
	}
	t.Cleanup(func() { CloseLogger(); InitLogger() })

	Logger().Info("info-message")
	Logger().Warn("warn-message")
	Logger().Error("error-message")
	CloseLogger()

	today := time.Now().Format(dayLayout)
	info := readFile(t, filepath.Join(dir, "info-"+today+".log"))
	warn := readFile(t, filepath.Join(dir, "warn-"+today+".log"))
	errLog := readFile(t, filepath.Join(dir, "error-"+today+".log"))

	if !strings.Contains(info, "info-message") || strings.Contains(info, "warn-message") || strings.Contains(info, "error-message") {
		t.Errorf("info file must contain only info:\n%s", info)
	}
	if !strings.Contains(warn, "warn-message") || strings.Contains(warn, "info-message") || strings.Contains(warn, "error-message") {
		t.Errorf("warn file must contain only warn:\n%s", warn)
	}
	if !strings.Contains(errLog, "error-message") || strings.Contains(errLog, "warn-message") {
		t.Errorf("error file must contain only error+:\n%s", errLog)
	}
	if !strings.Contains(errLog, `"stacktrace"`) {
		t.Error("error entries should carry a stacktrace")
	}
	if !strings.Contains(info, `"level":"info"`) || !strings.Contains(info, `"ts":"`+today+"T") {
		t.Errorf("expected JSON with ISO8601 time:\n%s", info)
	}
	// 软链接指向当天文件
	if got := readFile(t, filepath.Join(dir, "error.log")); got != errLog {
		t.Error("error.log symlink should point at today's error file")
	}
}

func TestDailyFileRotatesAndCleansUp(t *testing.T) {
	dir := t.TempDir()
	clock := time.Date(2026, 10, 1, 23, 59, 0, 0, time.Local)
	w := newDailyFile(dir, "info", 3)
	w.now = func() time.Time { return clock }
	defer w.Close()

	// 早于保留期的旧文件、格式不符的文件、其它级别的文件
	for _, name := range []string{"info-2026-09-01.log", "info-2026-09-29.log", "info-notes.log", "warn-2026-09-01.log"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("old\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := w.Write([]byte("day1\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	clock = clock.Add(2 * time.Minute) // 跨天
	if _, err := w.Write([]byte("day2\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	if got := readFile(t, filepath.Join(dir, "info-2026-10-01.log")); got != "day1\n" {
		t.Errorf("day1 file = %q", got)
	}
	if got := readFile(t, filepath.Join(dir, "info-2026-10-02.log")); got != "day2\n" {
		t.Errorf("day2 file = %q", got)
	}
	if got := readFile(t, filepath.Join(dir, "info.log")); got != "day2\n" {
		t.Errorf("info.log symlink should follow the current day, got %q", got)
	}
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(dir, name))
		return err == nil
	}
	if exists("info-2026-09-01.log") || exists("info-2026-09-29.log") {
		t.Error("files older than retention should be removed")
	}
	if !exists("info-notes.log") || !exists("warn-2026-09-01.log") {
		t.Error("cleanup must only touch this level's dated files")
	}
}

func TestDailyFileRetentionZeroKeepsEverything(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "error-2020-01-01.log")
	if err := os.WriteFile(old, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := newDailyFile(dir, "error", 0)
	defer w.Close()
	if _, err := w.Write([]byte("x\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := os.Stat(old); err != nil {
		t.Error("retention 0 must not delete old files")
	}
}

func TestSetupLoggerConsoleOnly(t *testing.T) {
	if err := SetupLogger(LogOptions{Console: true}); err != nil {
		t.Fatalf("SetupLogger: %v", err)
	}
	t.Cleanup(func() { CloseLogger(); InitLogger() })
	Logger().Info("console only") // 不应 panic,也不创建文件
}
