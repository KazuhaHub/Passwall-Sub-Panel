package ports

import "testing"

func TestUISettingsBrandName(t *testing.T) {
	cases := []struct {
		name string
		s    UISettings
		want string
	}{
		{"site title preferred", UISettings{SiteTitle: "Kazuha Hub Passwall", AppTitle: "Passwall"}, "Kazuha Hub Passwall"},
		{"falls back to app title", UISettings{SiteTitle: "", AppTitle: "My Panel"}, "My Panel"},
		{"both empty falls back to default", UISettings{}, "Passwall"},
		{"whitespace site title is ignored", UISettings{SiteTitle: "   ", AppTitle: "App"}, "App"},
		{"whitespace both falls back to default", UISettings{SiteTitle: " ", AppTitle: " "}, "Passwall"},
	}
	for _, tc := range cases {
		if got := tc.s.BrandName(); got != tc.want {
			t.Errorf("%s: BrandName() = %q, want %q", tc.name, got, tc.want)
		}
	}
}
