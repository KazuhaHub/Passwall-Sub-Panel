// Package log is a thin wrapper over slog providing a single logger
// instance shared by the whole application.
package log

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

var defaultLogger *slog.Logger

func init() {
	defaultLogger = newLogger(slog.LevelInfo)
}

// SetLevel adjusts the global log level. Called from app startup once
// config is loaded.
func SetLevel(level slog.Level) {
	defaultLogger = newLogger(level)
}

func Info(msg string, args ...any)  { defaultLogger.Info(msg, args...) }
func Warn(msg string, args ...any)  { defaultLogger.Warn(msg, args...) }
func Error(msg string, args ...any) { defaultLogger.Error(msg, args...) }
func Debug(msg string, args ...any) { defaultLogger.Debug(msg, args...) }

// With returns a child logger with the given attributes pre-attached.
func With(args ...any) *slog.Logger { return defaultLogger.With(args...) }

func newLogger(level slog.Level) *slog.Logger {
	return slog.New(&xrayHandler{
		out:   os.Stdout,
		mu:    &sync.Mutex{},
		level: level,
	})
}

// xrayHandler keeps the useful structured attributes from slog while using
// the line-oriented shape operators already see from Xray:
//
// 2026/09/17 08:15:36.091882 [Info] passwall-sub-panel: message key=value
//
// A custom handler is used instead of slog.TextHandler because TextHandler
// puts the timestamp and level in key=value fields, which would leave the
// panel and its managed core visibly inconsistent in the journal.
type xrayHandler struct {
	out   io.Writer
	mu    *sync.Mutex
	level slog.Level
	attrs []boundAttr
	group []string
}

type boundAttr struct {
	attr  slog.Attr
	group []string
}

func (h *xrayHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *xrayHandler) Handle(_ context.Context, record slog.Record) error {
	message := sanitizeMessage(record.Message)
	attrs := make([]formattedAttr, 0, len(h.attrs)+record.NumAttrs())
	for _, bound := range h.attrs {
		appendAttr(&attrs, bound.attr, bound.group)
	}
	record.Attrs(func(attr slog.Attr) bool {
		appendAttr(&attrs, attr, h.group)
		return true
	})

	var line strings.Builder
	line.Grow(96 + len(message))
	line.WriteString(record.Time.UTC().Format("2006/01/02 15:04:05.000000"))
	line.WriteString(" [")
	line.WriteString(levelName(record.Level))
	line.WriteString("] passwall-sub-panel: ")
	line.WriteString(message)
	for _, attr := range attrs {
		line.WriteByte(' ')
		line.WriteString(attr.key)
		line.WriteByte('=')
		line.WriteString(attr.value)
	}
	line.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.out, line.String())
	return err
}

func (h *xrayHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	copyHandler := *h
	copyHandler.attrs = append([]boundAttr(nil), h.attrs...)
	for _, attr := range attrs {
		copyHandler.attrs = append(copyHandler.attrs, boundAttr{
			attr:  attr,
			group: append([]string(nil), h.group...),
		})
	}
	return &copyHandler
}

func (h *xrayHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	copyHandler := *h
	copyHandler.group = append(append([]string(nil), h.group...), name)
	return &copyHandler
}

type formattedAttr struct {
	key   string
	value string
}

func appendAttr(dst *[]formattedAttr, attr slog.Attr, group []string) {
	attr.Value = attr.Value.Resolve()
	if attr.Key == "" && attr.Value.Kind() == slog.KindAny && attr.Value.Any() == nil {
		return
	}
	if attr.Value.Kind() == slog.KindGroup {
		nextGroup := group
		if attr.Key != "" {
			nextGroup = append(append([]string(nil), group...), attr.Key)
		}
		for _, child := range attr.Value.Group() {
			appendAttr(dst, child, nextGroup)
		}
		return
	}
	if attr.Key == "" {
		return
	}
	key := strings.Join(append(append([]string(nil), group...), attr.Key), ".")
	*dst = append(*dst, formattedAttr{key: key, value: formatValue(attr.Value)})
}

func formatValue(value slog.Value) string {
	switch value.Kind() {
	case slog.KindString:
		return quoteValue(value.String())
	case slog.KindBool:
		return strconv.FormatBool(value.Bool())
	case slog.KindInt64:
		return strconv.FormatInt(value.Int64(), 10)
	case slog.KindUint64:
		return strconv.FormatUint(value.Uint64(), 10)
	case slog.KindFloat64:
		return strconv.FormatFloat(value.Float64(), 'g', -1, 64)
	case slog.KindDuration:
		return value.Duration().String()
	case slog.KindTime:
		return quoteValue(value.Time().UTC().Format(time.RFC3339Nano))
	case slog.KindAny:
		if value.Any() == nil {
			return "null"
		}
		return quoteValue(fmt.Sprint(value.Any()))
	default:
		return quoteValue(value.String())
	}
}

func quoteValue(value string) string {
	if value != "" && strings.IndexFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune(`"'=\\`, r)
	}) == -1 {
		return value
	}
	return strconv.Quote(value)
}

func sanitizeMessage(message string) string {
	return strings.NewReplacer("\r\n", `\n`, "\n", `\n`, "\r", `\r`).Replace(message)
}

func levelName(level slog.Level) string {
	switch {
	case level <= slog.LevelDebug:
		return "Debug"
	case level < slog.LevelWarn:
		return "Info"
	case level < slog.LevelError:
		return "Warning"
	default:
		return "Error"
	}
}
