package util

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const dayLayout = "2006-01-02"

// dailyFile 按天切分的日志文件:{dir}/{name}-YYYY-MM-DD.log,按服务器本地时间跨天切换。
// {dir}/{name}.log 是指向当天文件的软链接,便于 `tail -F logs/error.log`。
// retentionDays > 0 时,切换文件时只保留包括当天在内最近 retentionDays 天的文件。
type dailyFile struct {
	mu            sync.Mutex
	dir           string
	name          string
	retentionDays int
	now           func() time.Time

	day  string
	file *os.File
}

func newDailyFile(dir, name string, retentionDays int) *dailyFile {
	return &dailyFile{dir: dir, name: name, retentionDays: retentionDays, now: time.Now}
}

func (w *dailyFile) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if day := w.now().Format(dayLayout); w.file == nil || day != w.day {
		if err := w.rotate(day); err != nil {
			return 0, err
		}
	}
	return w.file.Write(p)
}

func (w *dailyFile) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	return w.file.Sync()
}

func (w *dailyFile) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func (w *dailyFile) path(day string) string {
	return filepath.Join(w.dir, fmt.Sprintf("%s-%s.log", w.name, day))
}

func (w *dailyFile) rotate(day string) error {
	f, err := os.OpenFile(w.path(day), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	if w.file != nil {
		_ = w.file.Close()
	}
	w.file, w.day = f, day

	link := filepath.Join(w.dir, w.name+".log")
	_ = os.Remove(link)
	_ = os.Symlink(filepath.Base(w.path(day)), link)

	w.cleanup(day)
	return nil
}

// cleanup 删除 {name}-YYYY-MM-DD.log 中早于保留期的文件;文件名不符合格式的一律不动。
func (w *dailyFile) cleanup(today string) {
	if w.retentionDays <= 0 {
		return
	}
	current, err := time.ParseInLocation(dayLayout, today, time.Local)
	if err != nil {
		return
	}
	// 保留包括今天在内的 retentionDays 天
	cutoff := current.AddDate(0, 0, -(w.retentionDays - 1))
	matches, _ := filepath.Glob(filepath.Join(w.dir, w.name+"-*.log"))
	for _, m := range matches {
		date := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(m), w.name+"-"), ".log")
		d, err := time.ParseInLocation(dayLayout, date, time.Local)
		if err == nil && d.Before(cutoff) {
			_ = os.Remove(m)
		}
	}
}
