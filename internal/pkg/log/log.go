// Package log is a thin wrapper over slog providing a single logger
// instance shared by the whole application.
package log

import (
	"log/slog"
	"os"
)

var defaultLogger *slog.Logger

func init() {
	defaultLogger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
}

// SetLevel adjusts the global log level. Called from app startup once
// config is loaded.
func SetLevel(level slog.Level) {
	defaultLogger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	}))
}

func Info(msg string, args ...any)  { defaultLogger.Info(msg, args...) }
func Warn(msg string, args ...any)  { defaultLogger.Warn(msg, args...) }
func Error(msg string, args ...any) { defaultLogger.Error(msg, args...) }
func Debug(msg string, args ...any) { defaultLogger.Debug(msg, args...) }

// With returns a child logger with the given attributes pre-attached.
func With(args ...any) *slog.Logger { return defaultLogger.With(args...) }
