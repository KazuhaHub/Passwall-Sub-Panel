package domain

import "testing"

func TestDestResolve_ValidAndNormalize(t *testing.T) {
	for _, v := range []DestResolve{DestResolveAsIs, DestResolveOnDemand} {
		if !v.Valid() {
			t.Fatalf("%q must be valid", v)
		}
	}
	for _, v := range []DestResolve{"", "if_non_match", "ip_on_demand", "ON_DEMAND"} {
		if v.Valid() {
			t.Fatalf("%q must not be valid", v)
		}
	}
	cases := map[DestResolve]DestResolve{
		"":              DestResolveAsIs, // a row from before the column existed
		DestResolveAsIs: DestResolveAsIs,
		"on_demand":     DestResolveOnDemand,
		"junk":          DestResolveAsIs, // junk never switches DNS on
		"if_non_match":  DestResolveAsIs,
	}
	for in, want := range cases {
		if got := NormalizeDestResolve(in); got != want {
			t.Errorf("NormalizeDestResolve(%q) = %q, want %q", in, got, want)
		}
		if got := in.Normalized(); got != want {
			t.Errorf("(%q).Normalized() = %q, want %q", in, got, want)
		}
	}
}
