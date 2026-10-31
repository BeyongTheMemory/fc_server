package util

import (
	"fmt"
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	logger    *zap.Logger
	logCloser func()
)

// LogOptions 日志输出配置。
type LogOptions struct {
	// Dir 日志目录;非空时按级别写入 info/warn/error 三类按天切分的文件。
	Dir string
	// Console 是否同时输出到 stderr(本地运行时便于在终端查看)。
	Console bool
	// RetentionDays 日志文件保留天数,0 表示不清理。
	RetentionDays int
}

// InitLogger 只输出到 stderr 的默认 logger(测试与早期启动使用)。
func InitLogger() {
	logger = zap.New(zapcore.NewCore(newEncoder(), zapcore.Lock(os.Stderr), zap.InfoLevel),
		zap.AddCaller(), zap.AddStacktrace(zap.ErrorLevel))
}

// SetupLogger 按级别分文件输出:
//
//	{Dir}/info-YYYY-MM-DD.log   只含 info
//	{Dir}/warn-YYYY-MM-DD.log   只含 warn
//	{Dir}/error-YYYY-MM-DD.log  error 及以上(含 panic/fatal,附调用栈)
//
// 并维护 info.log/warn.log/error.log 软链接指向当天文件。
func SetupLogger(opts LogOptions) error {
	enc := newEncoder()
	var cores []zapcore.Core
	var files []*dailyFile
	if opts.Dir != "" {
		if err := os.MkdirAll(opts.Dir, 0o755); err != nil {
			return fmt.Errorf("create log dir: %w", err)
		}
		levels := []struct {
			name    string
			enabled zap.LevelEnablerFunc
		}{
			{"info", func(l zapcore.Level) bool { return l == zapcore.InfoLevel }},
			{"warn", func(l zapcore.Level) bool { return l == zapcore.WarnLevel }},
			{"error", func(l zapcore.Level) bool { return l >= zapcore.ErrorLevel }},
		}
		for _, lv := range levels {
			f := newDailyFile(opts.Dir, lv.name, opts.RetentionDays)
			files = append(files, f)
			cores = append(cores, zapcore.NewCore(enc, f, lv.enabled))
		}
	}
	if opts.Console || len(cores) == 0 {
		cores = append(cores, zapcore.NewCore(enc, zapcore.Lock(os.Stderr), zap.InfoLevel))
	}
	logger = zap.New(zapcore.NewTee(cores...), zap.AddCaller(), zap.AddStacktrace(zap.ErrorLevel))
	logCloser = func() {
		for _, f := range files {
			_ = f.Close()
		}
	}
	return nil
}

// CloseLogger 刷盘并关闭日志文件,进程退出前调用。
func CloseLogger() {
	if logger != nil {
		_ = logger.Sync()
	}
	if logCloser != nil {
		logCloser()
	}
}

// newEncoder JSON 格式,时间为带时区的 ISO8601(如 2026-10-31T10:00:00.000+0800)。
func newEncoder() zapcore.Encoder {
	cfg := zap.NewProductionEncoderConfig()
	cfg.EncodeTime = zapcore.ISO8601TimeEncoder
	return zapcore.NewJSONEncoder(cfg)
}

// SetLogger 替换全局 logger(测试中用于捕获日志)。
func SetLogger(l *zap.Logger) {
	logger = l
}

func Logger() *zap.Logger {
	return logger
}

func InfoLog(msg string, fields ...zap.Field) {
	Logger().Info(msg, fields...)
}
