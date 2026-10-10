package app

import (
	"reflect"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestDestinationBaseHostsFollowCurrentDefaultTemplateEdits(t *testing.T) {
	a := buildDestinationListsFixture(t)
	destinationRefreshAdminToken(t, a)
	got, err := a.destinationGroupBaseHosts(t.Context())
	if err != nil || !reflect.DeepEqual(got, []string{"dns.alidns.com", "l9f26nnn5d.cloudflare-gateway.com"}) {
		t.Fatalf("current template hosts=%v: %v", got, err)
	}
	for _, client := range []domain.ClientType{domain.ClientMihomo, domain.ClientSingBox} {
		template, err := a.repos.Template.GetDefault(t.Context(), client)
		if err != nil {
			t.Fatal(err)
		}
		changed := *template
		changed.Content = "dns: {}"
		if client == domain.ClientMihomo {
			changed.Content = "dns:\n  nameserver-policy:\n    foreign: 'https://operator-dns.example.test/dns-query#Proxy'\n"
		}
		if err := a.repos.Template.Save(t.Context(), &changed); err != nil {
			t.Fatal(err)
		}
	}
	got, err = a.destinationGroupBaseHosts(t.Context())
	if err != nil || !reflect.DeepEqual(got, []string{"operator-dns.example.test"}) {
		t.Fatal("base host suggestions used stale cache or hardcoded seed examples")
	}
	template, err := a.repos.Template.GetDefault(t.Context(), domain.ClientMihomo)
	if err != nil {
		t.Fatal(err)
	}
	changed := *template
	changed.Content = "dns: ["
	if err := a.repos.Template.Save(t.Context(), &changed); err != nil {
		t.Fatal(err)
	}
	if got, err := a.destinationGroupBaseHosts(t.Context()); err == nil || got != nil {
		t.Fatal("malformed current template returned partial or fabricated DNS suggestions")
	}
}
