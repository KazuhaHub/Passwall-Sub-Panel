package domain

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestLegalLocaleChain(t *testing.T) {
	for _, c := range []struct {
		lang string
		want []string
	}{
		{"zh-TW", []string{"zh-TW", "zh-CN", "en-US"}},
		{"zh-CN", []string{"zh-CN", "en-US"}},
		{"en-US", []string{"en-US", "zh-CN"}},
		{"fr-FR", []string{"fr-FR", "en-US", "zh-CN"}},
		{"", []string{"en-US", "zh-CN"}},
		{"not a locale!", []string{"en-US", "zh-CN"}},
		{"zh-tw", []string{"zh-TW", "zh-CN", "en-US"}},
	} {
		if got := LegalLocaleChain(c.lang); !reflect.DeepEqual(got, c.want) {
			t.Errorf("LegalLocaleChain(%q) = %v, want %v", c.lang, got, c.want)
		}
	}
}

func TestNormalizeLegalLocale(t *testing.T) {
	for in, want := range map[string]string{"zh-cn": "zh-CN", "EN-us": "en-US", "ja": "ja", " zh-TW ": "zh-TW"} {
		if got, ok := NormalizeLegalLocale(in); !ok || got != want {
			t.Errorf("NormalizeLegalLocale(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "x", "zh_CN", "zh-CN-extra-extra-extra-long", strings.Repeat("a", 17), "zh-C N"} {
		if _, ok := NormalizeLegalLocale(bad); ok {
			t.Errorf("NormalizeLegalLocale(%q) accepted", bad)
		}
	}
}

func TestParseLegalKind(t *testing.T) {
	for _, k := range []string{"terms", "privacy"} {
		if got, ok := ParseLegalKind(k); !ok || string(got) != k {
			t.Errorf("ParseLegalKind(%q) = %q, %v", k, got, ok)
		}
	}
	for _, k := range []string{"", "Terms", "cookies", "../x"} {
		if _, ok := ParseLegalKind(k); ok {
			t.Errorf("ParseLegalKind(%q) accepted", k)
		}
	}
}

func TestValidateLegalContent(t *testing.T) {
	if err := ValidateLegalContent(strings.Repeat("a", LegalContentMaxBytes)); err != nil {
		t.Errorf("exactly the limit: %v", err)
	}
	if err := ValidateLegalContent(strings.Repeat("a", LegalContentMaxBytes+1)); !errors.Is(err, ErrLegalContentTooLarge) || !errors.Is(err, ErrValidation) {
		t.Errorf("60001 bytes: %v, want ErrLegalContentTooLarge (a validation error)", err)
	}
	// The limit is bytes, not characters: 20001 CJK characters are 60003 bytes.
	if err := ValidateLegalContent(strings.Repeat("条", 20001)); !errors.Is(err, ErrLegalContentTooLarge) {
		t.Errorf("20001 CJK characters: %v, want too large", err)
	}
	if err := ValidateLegalContent("  \n"); !errors.Is(err, ErrValidation) {
		t.Errorf("blank content: %v, want a validation error", err)
	}
}
