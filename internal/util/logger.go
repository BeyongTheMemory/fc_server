package util

import (
	"go.uber.org/zap"
)

var logger *zap.Logger

func InitLogger() {
	logger, _ = zap.NewProduction()
}

func Logger() *zap.Logger {
	return logger
}

func InfoLog(msg string, fields ...zap.Field) {
	Logger().Info(msg, fields...)
}
