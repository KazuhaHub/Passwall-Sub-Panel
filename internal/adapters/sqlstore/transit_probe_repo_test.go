package sqlstore

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func probeRow(panel int64, key, kind string, state domain.ProbeHealth, withheld bool, validUntil time.Time) domain.ProbeState {
	return domain.ProbeState{
		PanelID: panel, ObjectKey: key, Kind: kind, Attempts: 20, Failures: 20, State: state, Consecutive: 3,
		Withheld: withheld, StateSince: validUntil.Add(-time.Hour), ReportedAt: validUntil.Add(-time.Minute), ValidUntil: validUntil,
	}
}

func TestTransitProbeRepo_ApplyListPruneClear(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	repo := &transitProbeRepo{db: db}
	soon := time.Now().Add(time.Hour).UTC().Truncate(time.Second)

	put := func(panel int64, key, kind string, withheld bool) (*domain.ProbeState, domain.ProbeState) {
		t.Helper()
		prev, next, err := repo.Apply(ctx, panel, key, kind, func(prev *domain.ProbeState) (domain.ProbeState, bool, error) {
			return probeRow(0, "", "", domain.ProbeDown, withheld, soon), true, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return prev, next
	}
	if prev, next := put(1, "lnk_1", "e2e", false); prev != nil || next.PanelID != 1 || next.ObjectKey != "lnk_1" || next.Kind != "e2e" {
		t.Fatalf("first write: prev %v next %+v", prev, next)
	}
	if prev, _ := put(1, "lnk_1", "e2e", true); prev == nil || prev.Withheld {
		t.Fatalf("second write saw prev %+v, want the first row", prev)
	}
	put(1, "lnk_1", "transport", false)
	put(1, "fwd_4", "forward", true)
	put(2, "lnk_1", "e2e", true)

	// A declined update stores nothing and returns the row as it is.
	prev, next, err := repo.Apply(ctx, 1, "lnk_1", "e2e", func(*domain.ProbeState) (domain.ProbeState, bool, error) {
		return domain.ProbeState{}, false, nil
	})
	if err != nil || prev == nil || !next.Withheld {
		t.Fatalf("declined update: %v %+v %v", prev, next, err)
	}
	// An error from update stores nothing.
	boom := errors.New("boom")
	if _, _, err := repo.Apply(ctx, 1, "lnk_9", "e2e", func(*domain.ProbeState) (domain.ProbeState, bool, error) {
		return domain.ProbeState{}, true, boom
	}); !errors.Is(err, boom) {
		t.Fatalf("update error = %v", err)
	}

	rows, err := repo.List(ctx)
	if err != nil || len(rows) != 4 {
		t.Fatalf("List = %d rows, %v", len(rows), err)
	}
	if !rows[0].ValidUntil.Equal(soon) || rows[0].Attempts != 20 {
		t.Fatalf("row did not round trip: %+v", rows[0])
	}

	if n, err := repo.PruneExcept(ctx, 1, []string{"lnk_1"}); err != nil || n != 1 {
		t.Fatalf("PruneExcept = %d, %v, want the forward gone", n, err)
	}
	if n, err := repo.PruneExcept(ctx, 2, nil); err != nil || n != 1 {
		t.Fatalf("PruneExcept with nothing kept = %d, %v", n, err)
	}
	if n, err := repo.ClearWithheld(ctx); err != nil || n != 1 {
		t.Fatalf("ClearWithheld = %d, %v, want the one remaining flag", n, err)
	}
	rows, _ = repo.List(ctx)
	for _, row := range rows {
		if row.Withheld {
			t.Fatalf("a flag survived: %+v", row)
		}
	}
}

// The loaders fill the probe fields: a down, withheld, fresh row withholds; a
// stale one and an unknown one never do; another server's row is not this
// line's.
func TestAttachProbeState(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	repo := &transitProbeRepo{db: db}
	fresh, old := time.Now().Add(time.Hour), time.Now().Add(-time.Hour)

	link := transitLinkRow{EntryPanelID: 5, ExitPanelID: 6, Protocol: "ss2022", Port: 1, SSMethod: "m", SSKey: "k"}
	if err := db.Create(&link).Error; err != nil {
		t.Fatal(err)
	}
	store := func(row domain.ProbeState) {
		t.Helper()
		if _, _, err := repo.Apply(ctx, row.PanelID, row.ObjectKey, row.Kind, func(*domain.ProbeState) (domain.ProbeState, bool, error) { return row, true, nil }); err != nil {
			t.Fatal(err)
		}
	}
	key := "lnk_" + strconv.FormatInt(link.ID, 10)
	store(probeRow(5, key, domain.ProbeKindE2E, domain.ProbeDown, true, fresh))
	store(probeRow(5, "fwd_7", domain.ProbeKindForward, domain.ProbeDown, true, fresh))
	store(probeRow(5, "fwd_8", domain.ProbeKindForward, domain.ProbeDown, true, old))
	store(probeRow(5, "fwd_9", domain.ProbeKindForward, domain.ProbeUnknown, true, fresh))
	store(probeRow(99, "fwd_7", domain.ProbeKindForward, domain.ProbeDown, true, fresh))

	// Withholding is on: a flag counts only then.
	for name, value := range map[string]string{"withhold": "1", "off": "0"} {
		if err := db.Create(&settingRow{Type: "transit_probe", Name: name, Value: value}).Error; err != nil {
			t.Fatal(err)
		}
	}
	node := &domain.Node{ID: 1, PanelID: 5, EgressPanelID: 6, Relays: []domain.RelayLine{
		{ID: "a", EntryPanelID: 5, Enabled: true}, {ID: "b", EntryPanelID: 5, Enabled: true},
		{ID: "c", EntryPanelID: 5, Enabled: true}, {ID: "d", EntryPanelID: 6, Enabled: true},
		{ID: "x", Enabled: true},
	}}
	ids := map[probeLineRef]int64{{1, "a"}: 7, {1, "b"}: 8, {1, "c"}: 9, {1, "d"}: 7}
	if err := attachProbeState(ctx, db, []*domain.Node{node}, ids); err != nil {
		t.Fatal(err)
	}
	if !node.EgressWithheld || node.EgressProbe == nil || node.EgressProbe.State != domain.ProbeDown {
		t.Fatalf("tunnel node: withheld %v probe %+v", node.EgressWithheld, node.EgressProbe)
	}
	want := map[string]struct {
		withheld bool
		state    domain.ProbeHealth
	}{"a": {true, domain.ProbeDown}, "b": {false, domain.ProbeUnknown}, "c": {false, domain.ProbeUnknown}}
	for _, relay := range node.Relays {
		w, ok := want[relay.ID]
		switch {
		case ok && (relay.ProbeWithheld != w.withheld || relay.Probe == nil || relay.Probe.State != w.state):
			t.Errorf("line %s: withheld %v probe %+v, want %+v", relay.ID, relay.ProbeWithheld, relay.Probe, w)
		case !ok && (relay.ProbeWithheld || relay.Probe != nil):
			t.Errorf("line %s has no row of its own but got withheld %v probe %+v", relay.ID, relay.ProbeWithheld, relay.Probe)
		}
	}
	// With withholding off (or probing off) a stored flag withholds nothing,
	// whatever raced the settings write.
	for _, off := range []map[string]string{{"withhold": "0", "off": "0"}, {"withhold": "1", "off": "1"}} {
		for name, value := range off {
			if err := db.Model(&settingRow{}).Where("type = ? AND name = ?", "transit_probe", name).Update("value", value).Error; err != nil {
				t.Fatal(err)
			}
		}
		if err := attachProbeState(ctx, db, []*domain.Node{node}, ids); err != nil {
			t.Fatal(err)
		}
		if node.EgressWithheld || node.Relays[0].ProbeWithheld || node.Relays[0].Probe == nil || node.Relays[0].Probe.Withheld {
			t.Fatalf("settings %v: a stored flag withheld with the setting off", off)
		}
	}
	// Stale state is reset on a reload, not carried over.
	node.EgressWithheld = true
	if err := attachProbeState(ctx, db, []*domain.Node{{ID: 2, PanelID: 5, Relays: []domain.RelayLine{{ID: "z", EntryPanelID: 5}}}}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestTransitProbeSettingsPersist(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	repo := newKVSettingsRepo(db)
	fresh, err := repo.Load(ctx, ports.UISettings{})
	if err != nil {
		t.Fatal(err)
	}
	if !fresh.TransitProbeSettings().TransitProbeEnabled || ports.TransitProbeEffectiveOf(fresh).Withhold {
		t.Fatalf("a database that never saw the keys must probe and not withhold: %+v", fresh.TransitProbeSettings())
	}
	want := ports.TransitProbeSettings{TransitProbeEnabled: false, TransitProbeIntervalSeconds: 45, TransitProbeTimeoutMS: 800,
		TransitProbeURL: "http://probe.example:8080/ok", TransitProbeWithhold: true, TransitProbeWithholdAfter: 5, TransitProbeRestoreAfter: 2}
	fresh.SetTransitProbeSettings(want)
	if err := repo.Save(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Load(ctx, ports.UISettings{})
	if err != nil || got.TransitProbeSettings() != want {
		t.Fatalf("loaded %+v, %v, want %+v", got.TransitProbeSettings(), err, want)
	}
}
