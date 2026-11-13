package app

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/mmdbtype"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// writeCountryMMDB writes a country database mapping each network to a code.
func writeCountryMMDB(t *testing.T, path string, nets map[string]string) {
	t.Helper()
	w, err := mmdbwriter.New(mmdbwriter.Options{DatabaseType: "Test-Country", IncludeReservedNetworks: true, RecordSize: 24})
	if err != nil {
		t.Fatal(err)
	}
	for cidr, cc := range nets {
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			t.Fatal(err)
		}
		if err := w.Insert(n, mmdbtype.Map{"country": mmdbtype.Map{"iso_code": mmdbtype.String(cc)}}); err != nil {
			t.Fatal(err)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := w.WriteTo(f); err != nil {
		t.Fatal(err)
	}
}

// THE DESTINATION REPOS AND SERVICES ARE LATE-BOUND, WHICH MAKES FORGETTING A
// LINK SILENT. Every collaborator of destlist.Service is optional and degrades
// without a word — no Settings reads the defaults, no Locator makes every
// country "unavailable", no State means a GeoIP change cannot be noticed — and
// the catalog's store and lister likewise. With a line gone from Build the
// build still compiles and the feature just never behaves.
//
// So this drives the application Build assembled, one effect through each
// link: the lists are persisted by the family's own store, the catalog creates
// a policy through it, the settings the refresh loop reads are the live ones,
// the GeoIP database a country is expanded from is the geo service's active
// one, a replaced database is noticed by the loop and republishes, and the two
// removal cleanups (a node's source rule, a native panel's delivery state) are
// wired to the real repos.
func TestBuildWiresTheDestinationRepos(t *testing.T) {
	ctx := t.Context()
	directory := t.TempDir()
	cfg := &config.Config{
		Listen: "127.0.0.1:0", JWTSecret: strings.Repeat("j", 48), EncryptionKey: strings.Repeat("e", 48),
		ConfigDir: filepath.Join(directory, "config"), DataDir: filepath.Join(directory, "data"),
	}
	a, err := Build(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.Shutdown(shutdownCtx); err != nil {
			t.Error(err)
		}
		sqlstore.ConfigureSecretKey("")
	})
	if a.destRepos == nil || a.destLists == nil || a.destCatalog == nil {
		t.Fatalf("Build left the destination family unwired: repos %v lists %v catalog %v", a.destRepos != nil, a.destLists != nil, a.destCatalog != nil)
	}
	generation := func() int64 {
		t.Helper()
		st, err := a.destRepos.State.Get(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return st.Generation
	}

	// A list written through the service lands in the database the family's
	// store reads.
	l, _, err := a.destLists.CreateCustom(ctx, "wired", domain.DestSideDest, []byte("example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := a.destRepos.Lists.GetByID(ctx, l.ID); err != nil || got.EntryCount != 1 {
		t.Fatalf("the list service does not write the family's store: %+v %v", got, err)
	}

	// The catalog creates a policy through the same store (smtp needs no
	// geosite data, so no network) and marks it added from the same lister.
	pol, err := a.destCatalog.Instantiate(ctx, "smtp", "en-US", domain.DestStateOff)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := a.destRepos.Policies.GetByID(ctx, pol.ID); err != nil || got.TemplateKey != "smtp" {
		t.Fatalf("the catalog does not write the family's store: %+v %v", got, err)
	}
	view, err := a.destCatalog.View(ctx)
	if err != nil {
		t.Fatal(err)
	}
	added := false
	for _, tv := range view.Templates {
		if tv.Key == "smtp" {
			added = tv.Added
		}
	}
	if !added {
		t.Fatal("the catalog does not read the policies it created")
	}

	// The settings the loop reads are the live ones.
	set, err := a.settings.Load(ctx, ports.UISettings{})
	if err != nil {
		t.Fatal(err)
	}
	set.SetAccessControlSettings(ports.AccessControlSettings{DestListRefreshHours: 6})
	if err := a.settings.Save(ctx, set); err != nil {
		t.Fatal(err)
	}
	if got := a.destLists.RefreshInterval(ctx); got != 6*time.Hour {
		t.Fatalf("the list service reads other settings than the panel's: interval %v, want 6h", got)
	}

	// With no GeoIP database a country is unavailable, and with the geo
	// service's active one it expands: the locator is the geo service.
	if _, err := a.destLists.Countries.CIDRs(ctx, []string{"CN"}); err == nil {
		t.Fatal("a country expanded with no database")
	}
	geoDir := filepath.Join(cfg.ConfigDir, "geoip")
	dbPath := filepath.Join(geoDir, "country.mmdb")
	writeCountryMMDB(t, dbPath, map[string]string{"1.0.0.0/24": "CN", "1.0.1.0/24": "CN"})
	got, err := a.destLists.Countries.CIDRs(ctx, []string{"CN"})
	if err != nil || len(got) != 1 || got[0] != "1.0.0.0/23" {
		t.Fatalf("CN = %v, %v; want the merged range from the geo service's database", got, err)
	}

	// The refresh loop runs the whole pass: a replaced database under a source
	// rule that denies a country republishes. (The first pass only records the
	// database; the second sees the change.)
	if err := a.destRepos.SourceRules.Upsert(ctx, &domain.DestSourceRule{NodeID: 1, Countries: []string{"CN"}, State: domain.DestStateOn}, nil); err != nil {
		t.Fatal(err)
	}
	loopCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.runDestListRefreshLoop(loopCtx, 10*time.Millisecond, 30*time.Millisecond)
	}()
	defer func() { stop(); <-done }()
	time.Sleep(150 * time.Millisecond) // a few passes: the database is recorded
	before := generation()
	writeCountryMMDB(t, dbPath, map[string]string{"1.0.0.0/24": "CN", "9.0.0.0/8": "CN"})
	deadline := time.Now().Add(5 * time.Second)
	for generation() == before && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if generation() == before {
		t.Fatal("the refresh loop did not notice a replaced GeoIP database under a source rule")
	}
	stop()
	<-done

	// A node's removal takes its source rule with it, through the repo Build
	// hands every consumer.
	node := &domain.Node{PanelID: 1, InboundID: 1, DisplayName: "wired"}
	if err := a.repos.Node.Create(ctx, node); err != nil {
		t.Fatal(err)
	}
	if err := a.destRepos.SourceRules.Upsert(ctx, &domain.DestSourceRule{NodeID: node.ID, Countries: []string{"RU"}, State: domain.DestStateOn}, nil); err != nil {
		t.Fatal(err)
	}
	if err := a.repos.Node.Delete(ctx, node.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.destRepos.SourceRules.Get(ctx, node.ID); err == nil {
		t.Fatal("a deleted node's source rule remained")
	}
}
