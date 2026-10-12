//go:build destination_acceptance && linux

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type nativePageResult struct {
	report nativePageReport
	failed string
	query  int
}

func nativeStartPageProbe(ctx context.Context, client *http.Client, endpoint, token string, own nativePageIdentity, start time.Time) <-chan nativePageResult {
	done := make(chan nativePageResult, 1)
	go func() {
		ctx, cancel := context.WithDeadline(ctx, start.Add(300*time.Second))
		defer cancel()
		result := nativePageResult{report: nativePageReport{Schema: 1, Queries: []nativePageQuery{{Name: "hits_first_page", Samples: []nativePageSample{}}, {Name: "policies_overview", Samples: []nativePageSample{}}}}}
		defer func() { done <- result }()
		paths := []string{"/api/admin/dest/hits?group_by=none&include_trial=1&page=1&page_size=50&panel_id=" + strconv.FormatInt(own.panel, 10), "/api/admin/dest/policies"}
		// Keep a four-second end margin; every admitted request must complete in
		// the measured flood window. Never count scheduled or failed reads.
		for tick := 0; tick < 149; tick++ {
			due := start.Add(time.Duration(tick) * 2 * time.Second)
			if delay := time.Until(due); delay > 0 {
				timer := time.NewTimer(delay)
				select {
				case <-timer.C:
				case <-ctx.Done():
					timer.Stop()
					result.failed = "deadline"
					return
				}
			}
			for i, path := range paths {
				result.query = i
				elapsed, began := time.Since(start).Milliseconds(), time.Now()
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+path, nil)
				if err != nil {
					result.failed = "request"
					return
				}
				req.Header.Set("Authorization", "Bearer "+token)
				response, err := client.Do(req)
				if err != nil {
					result.failed = "transport"
					return
				}
				body, readErr := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
				closeErr := response.Body.Close()
				if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") || readErr != nil || closeErr != nil || len(body) > 1<<20 {
					result.failed = "response"
					return
				}
				items, total, valid := nativeValidatePage(body, i == 1, own)
				if !valid {
					result.failed = "identity"
					return
				}
				result.report.Queries[i].Samples = append(result.report.Queries[i].Samples, nativePageSample{ElapsedMS: elapsed, DurationUS: time.Since(began).Microseconds(), Items: items, Total: total})
			}
		}
	}()
	return done
}

func nativeWritePageProbe(t *testing.T, evidence string, duration int64, done <-chan nativePageResult) string {
	t.Helper()
	result := <-done
	result.report.WindowMS = duration
	data, err := json.Marshal(result.report)
	nativeMust(t, err, "encode numeric first-screen API evidence")
	nativeMust(t, os.WriteFile(filepath.Join(filepath.Dir(evidence), "destination-trial-load-pages.json"), append(data, '\n'), 0644), "write owned first-screen API evidence")
	if result.failed != "" {
		return fmt.Sprintf("phase=%s query=%d", result.failed, result.query)
	}
	return ""
}
