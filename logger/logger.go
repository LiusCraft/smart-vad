package logger

import (
	"log/slog"
	"os"
)

var l *slog.Logger

func init() {
	Init(false)
}

func Init(debug bool) {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}

	var handler slog.Handler
	if os.Getenv("LOG_FORMAT") == "json" {
		handler = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	} else {
		handler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	}
	l = slog.New(handler)
}

func Debug(msg string, args ...any) { l.Debug(msg, args...) }
func Info(msg string, args ...any)  { l.Info(msg, args...) }
func Warn(msg string, args ...any)  { l.Warn(msg, args...) }
func Error(msg string, args ...any) { l.Error(msg, args...) }
func Fatal(msg string, args ...any) {
	l.Error(msg, args...)
	os.Exit(1)
}
