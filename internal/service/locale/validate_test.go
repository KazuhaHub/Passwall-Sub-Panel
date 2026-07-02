package locale

import (
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func validPack() *domain.LocalePack {
	return &domain.LocalePack{
		Format: Format,
		Code:   "fr-FR",
		Name:   "Français",
		Namespaces: map[string]map[string]any{
			"common": {"app": map[string]any{"save": "Enregistrer"}},
			"nav":    {"home": "Accueil"},
		},
	}
}

func TestValidate_OK(t *testing.T) {
	if err := Validate(validPack()); err != nil {
		t.Fatalf("expected valid pack, got %v", err)
	}
}

func TestValidate_EmptyNamespacesOK(t *testing.T) {
	// A partial (even empty) pack is allowed — i18next fallback fills the gaps.
	p := validPack()
	p.Namespaces = map[string]map[string]any{}
	if err := Validate(p); err != nil {
		t.Fatalf("empty namespaces should be allowed, got %v", err)
	}
}

func TestValidate_BadFormat(t *testing.T) {
	p := validPack()
	p.Format = 999
	if err := Validate(p); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("expected ErrValidation for unknown format, got %v", err)
	}
	p.Format = 0
	if err := Validate(p); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("expected ErrValidation for zero format, got %v", err)
	}
}

func TestValidate_BadCode(t *testing.T) {
	for _, code := range []string{"", "../escape", "fr FR", "zh.CN", "a/b"} {
		p := validPack()
		p.Code = code
		if err := Validate(p); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("expected ErrValidation for code %q, got %v", code, err)
		}
	}
}

func TestValidate_ReservedCode(t *testing.T) {
	for _, code := range []string{"zh-CN", "en-US"} {
		p := validPack()
		p.Code = code
		if err := Validate(p); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("expected ErrValidation for reserved code %q, got %v", code, err)
		}
	}
}

func TestValidate_MissingName(t *testing.T) {
	p := validPack()
	p.Name = ""
	if err := Validate(p); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("expected ErrValidation for empty name, got %v", err)
	}
}

func TestValidate_UnknownNamespace(t *testing.T) {
	p := validPack()
	p.Namespaces["bogus"] = map[string]any{"x": "y"}
	if err := Validate(p); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("expected ErrValidation for unknown namespace, got %v", err)
	}
}

func TestValidate_NonStringLeaf(t *testing.T) {
	// Numbers, bools, arrays, and null must all be rejected so only
	// {string:string} ever reaches the SPA's t(). JSON decodes these as
	// float64 / bool / []any / nil respectively.
	cases := []any{
		float64(42),
		true,
		[]any{"a", "b"},
		nil,
		map[string]any{"deep": float64(1)}, // nested non-string leaf
	}
	for _, bad := range cases {
		p := validPack()
		p.Namespaces["common"] = map[string]any{"bad": bad}
		if err := Validate(p); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("expected ErrValidation for non-string leaf %#v, got %v", bad, err)
		}
	}
}

func TestIsReserved(t *testing.T) {
	if !IsReserved("zh-CN") || !IsReserved("en-US") {
		t.Fatal("built-in codes must be reserved")
	}
	if IsReserved("fr-FR") {
		t.Fatal("fr-FR must not be reserved")
	}
}
