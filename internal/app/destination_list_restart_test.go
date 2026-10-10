package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestBuildDestinationGeositeReportSurvivesDatabaseAndCacheReopen(t *testing.T) {
	directory := t.TempDir()
	cfg := &config.Config{Listen: "127.0.0.1:0", JWTSecret: strings.Repeat("j", 48), EncryptionKey: strings.Repeat("e", 48), ConfigDir: filepath.Join(directory, "config"), DataDir: filepath.Join(directory, "data")}
	cacheDir := filepath.Join(cfg.DataDir, "destlists")
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		t.Fatal(err)
	}
	const catalog = "lists:\n  - name: finance\n    length: 2\n    rules:\n      - domain:hsbc\n      - domain:hsbc.com\n"
	if err := os.WriteFile(filepath.Join(cacheDir, "dlc_plain.yml"), []byte(catalog), 0600); err != nil {
		t.Fatal(err)
	}
	var current *App
	closeApp := func() {
		t.Helper()
		if current == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		closing := current
		current = nil
		if err := closing.Shutdown(ctx); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		defer sqlstore.ConfigureSecretKey("")
		closeApp()
	})
	build := func() {
		t.Helper()
		var err error
		current, err = Build(t.Context(), cfg)
		if err != nil {
			t.Fatal(err)
		}
	}
	build()
	token := destinationRefreshAdminToken(t, current)
	w := destinationListRequest(t, current, token, "POST", "lists", map[string]any{"name": "persistent finance", "kind": "geosite", "geosite_category": "finance"})
	var created struct {
		ID int64 `json:"id"`
	}
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &created) != nil || created.ID == 0 {
		t.Fatal("filtered finance save fixture failed")
	}
	list, err := current.destDefinitions.GetList(t.Context(), created.ID)
	if err != nil || list.ParseReport == nil || list.ParseReport.IgnoredBroad != 1 {
		t.Fatal("saved finance report missing")
	}
	// Exercise the same durable commit boundary used by the background worker.
	// The newer successful report must survive even when the catalog is unchanged.
	next := domain.DestParseReport{Accepted: 1, Ignored: 2, IgnoredBroad: 2, Samples: []domain.DestParseSample{{Line: 2, Text: "domain:co.uk", Reason: "broad_entry"}}}
	if err := current.destDefinitions.CommitListRefresh(t.Context(), list, domain.DestListRefresh{Entries: list.Entries, ContentSHA256: list.ContentSHA256, EntryCount: list.EntryCount, ParseReport: &next}, time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	closeApp()
	build() // A fresh SQL pool, repos, service and cache, with no Run/network worker.
	w = destinationListRequest(t, current, token, "GET", fmt.Sprintf("lists/%d", list.ID), nil)
	var detail struct {
		Report domain.DestParseReport `json:"parse_report"`
		SHA    string                 `json:"content_sha256"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &detail) != nil || !reflect.DeepEqual(detail.Report, next) || detail.SHA != list.ContentSHA256 {
		t.Fatal("fresh app did not return the last committed successful detail report")
	}
	w = destinationListRequest(t, current, token, "GET", "lists", nil)
	var overview struct {
		Items []struct {
			ID     int64                  `json:"id"`
			Report domain.DestParseReport `json:"parse_report_summary"`
		} `json:"items"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &overview) != nil || len(overview.Items) != 1 || overview.Items[0].ID != list.ID || overview.Items[0].Report.Accepted != next.Accepted || overview.Items[0].Report.Ignored != next.Ignored || overview.Items[0].Report.IgnoredBroad != next.IgnoredBroad {
		t.Fatal("fresh overview did not expose the same successful report totals")
	}
	w = destinationListRequest(t, current, token, "POST", "lists/preview", map[string]any{"name": "cached finance", "kind": "geosite", "geosite_category": "finance"})
	var preview struct {
		SHA string `json:"content_sha256"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &preview) != nil || preview.SHA != list.ContentSHA256 {
		t.Fatal("fresh cache could not preview the same canonical finance content offline")
	}
}
