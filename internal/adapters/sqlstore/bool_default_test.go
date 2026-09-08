package sqlstore

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// GORM omits a zero-valued field from an INSERT when its column carries a
// default, so a plain `bool` with `default:true` CANNOT store false on create:
// the row comes back with the default. Nothing errors — the write "succeeds"
// and stores the opposite of what was asked for.
//
// This repository has now been bitten three times. geo_streak_repo documents
// the first (a partial count could not be recorded as partial, so every floor
// claimed to be a total). These are the other two families, and both are
// switches an operator flips ON PURPOSE:
//
//	a node created disabled came back enabled — and an enabled node is handed
//	straight to every subscription;
//	a separator created with the toggle off came back on;
//	a certificate created with auto-renew off came back with it on.
//
// The remedy is a *bool in the ROW struct only. The column default stays,
// because AutoMigrate uses it to backfill rows written before the column
// existed; the pointer is what stops Go's false from reading as "unset".
func TestBoolDefaultsCanStoreFalse(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	ctx := context.Background()

	t.Run("node", func(t *testing.T) {
		r := &nodeRepo{db: db}
		n := &domain.Node{
			PanelID: 1, InboundID: 1, DisplayName: "n",
			Region: "hk", ServerAddress: "a.example.com", Enabled: false,
		}
		if err := r.Create(ctx, n); err != nil {
			t.Fatal(err)
		}
		got, err := r.GetByID(ctx, n.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Enabled {
			t.Fatal("a node created disabled came back ENABLED — it would be served to every subscription")
		}
		// The pointer must not have broken the ordinary enable path.
		if err := r.UpdateEnabled(ctx, n.ID, true); err != nil {
			t.Fatal(err)
		}
		if got, _ = r.GetByID(ctx, n.ID); !got.Enabled {
			t.Fatal("enabling afterwards did not take")
		}
		if err := r.UpdateEnabled(ctx, n.ID, false); err != nil {
			t.Fatal(err)
		}
		if got, _ = r.GetByID(ctx, n.ID); got.Enabled {
			t.Fatal("disabling afterwards did not take")
		}
	})

	t.Run("separator", func(t *testing.T) {
		r := &separatorRepo{db: db}
		e := &domain.SeparatorEntry{DisplayName: "x", Enabled: false, Mode: domain.SeparatorModeGlobal}
		if err := r.Create(ctx, e); err != nil {
			t.Fatal(err)
		}
		got, err := r.GetByID(ctx, e.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Enabled {
			t.Fatal("a separator created with the toggle off came back ON")
		}
		// And the reported path — disabling an existing one — still works.
		e.Enabled = true
		if err := r.Update(ctx, e); err != nil {
			t.Fatal(err)
		}
		if got, _ = r.GetByID(ctx, e.ID); !got.Enabled {
			t.Fatal("enabling an existing separator did not take")
		}
		e.Enabled = false
		if err := r.Update(ctx, e); err != nil {
			t.Fatal(err)
		}
		if got, _ = r.GetByID(ctx, e.ID); got.Enabled {
			t.Fatal("disabling an existing separator did not take")
		}
	})

	t.Run("certificate", func(t *testing.T) {
		r := &certificateRepo{db: db}
		c := &domain.TLSCertificate{Name: "a", Domains: []string{"a.example.com"}, AutoRenew: false}
		if err := r.Create(ctx, c); err != nil {
			t.Fatal(err)
		}
		got, err := r.GetByID(ctx, c.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.AutoRenew {
			t.Fatal("a certificate created with auto-renew OFF came back with it on")
		}
	})
}

// Any future `bool` column carrying a default is the same trap. Requiring a
// pointer at the type level catches it at the moment the column is declared,
// rather than when an operator notices a switch that will not stay off.
func TestNoPlainBoolCarriesAColumnDefault(t *testing.T) {
	rows := []any{
		nodeRow{}, separatorRow{}, tlsCertificateRow{}, userRow{},
		groupRow{}, xuiPanelRow{}, syncTaskRow{}, dnsCredentialRow{},
	}
	for _, row := range rows {
		rt := reflect.TypeOf(row)
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			if f.Type.Kind() != reflect.Bool {
				continue
			}
			tag := f.Tag.Get("gorm")
			// default:false is harmless — false IS the zero value, so the
			// omitted INSERT and the explicit one agree.
			if strings.Contains(tag, "default:") && !strings.Contains(tag, "default:false") {
				t.Errorf("%s.%s is a plain bool with %q: it cannot store false on create — make it *bool",
					rt.Name(), f.Name, tag)
			}
		}
	}
}
