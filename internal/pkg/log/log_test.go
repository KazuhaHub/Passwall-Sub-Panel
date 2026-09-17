package log

import (
	"bytes"
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"
)

func TestXrayHandlerFormatsUTCLevelsAndAttributes(t *testing.T) {
	var output bytes.Buffer
	handler := &xrayHandler{
		out:   &output,
		mu:    &sync.Mutex{},
		level: slog.LevelDebug,
	}
	logger := slog.New(handler)

	logger.LogAttrs(context.Background(), slog.LevelWarn,
		"sync failed: line one\nline two",
		slog.String("address", "0.0.0.0:8788"),
		slog.Int("attempt", 2),
		slog.String("detail", "value with spaces"),
		slog.Group("node", slog.Int64("id", 7)),
	)

	// slog supplies the current time to a Record. The handler's timestamp
	// formatting is validated separately below with a directly constructed
	// Record so this assertion focuses on the line shape and escaping.
	got := output.String()
	if !bytes.Contains([]byte(got), []byte(" [Warning] passwall-sub-panel: sync failed: line one\\nline two")) {
		t.Fatalf("log output = %q", got)
	}
	for _, fragment := range []string{
		" address=0.0.0.0:8788",
		" attempt=2",
		` detail="value with spaces"`,
		" node.id=7",
	} {
		if !bytes.Contains([]byte(got), []byte(fragment)) {
			t.Fatalf("log output = %q, missing %q", got, fragment)
		}
	}

	output.Reset()
	record := slog.NewRecord(time.Date(2026, 9, 16, 3, 4, 5, 600, time.FixedZone("local", -7*60*60)), slog.LevelInfo, "ready", 0)
	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	wantPrefix := "2026/09/16 10:04:05.000000 [Info] passwall-sub-panel: ready\n"
	if got := output.String(); got != wantPrefix {
		t.Fatalf("timestamp output = %q, want %q", got, wantPrefix)
	}
}

func TestXrayHandlerHonorsLevel(t *testing.T) {
	var output bytes.Buffer
	handler := &xrayHandler{out: &output, mu: &sync.Mutex{}, level: slog.LevelInfo}
	logger := slog.New(handler)
	logger.Debug("hidden")
	logger.Info("visible")

	if got := output.String(); bytes.Contains([]byte(got), []byte("hidden")) || !bytes.Contains([]byte(got), []byte("visible")) {
		t.Fatalf("level filtering output = %q", got)
	}
}
