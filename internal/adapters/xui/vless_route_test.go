package xui

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRouteTemplateReadOnlyCatalogAndWrapperForms(t *testing.T) {
	raw := `{"xraySetting":{"outbounds":[{"tag":"warp","protocol":"socks","settings":{"password":"secret"}}],"routing":{"rules":[]},"unknown":9007199254740993},"outboundTestUrl":"https://example.test","subscriptionOutbounds":[{"tag":"remote","protocol":"vless","settings":{"id":"credential"}}],"subscriptionOutboundTags":["remote"]}`
	encoded, _ := json.Marshal(raw)
	for _, input := range []json.RawMessage{json.RawMessage(raw), encoded} {
		got, err := decodeRouteTemplate(input)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Outbounds) != 2 || got.Outbounds[0].Tag != "remote" || got.TestURL != "https://example.test" {
			t.Fatalf("bad metadata: %+v", got.Outbounds)
		}
		public, _ := json.Marshal(got.Outbounds)
		if strings.Contains(string(public), "secret") || strings.Contains(string(public), "credential") {
			t.Fatal("catalog leaked outbound settings")
		}
		if strings.Contains(string(public), `"source"`) {
			t.Fatal("unused outbound source leaked into the public catalog")
		}
		if !strings.Contains(string(got.Config), "9007199254740993") {
			t.Fatal("unknown numeric data lost precision")
		}
	}
}

func TestRouteTemplateRejectsMissingAndAmbiguousCatalog(t *testing.T) {
	for _, raw := range []string{`{}`, `null`, `{"xraySetting":null}`, `{"xraySetting":{"outbounds":[{"tag":"same"},{"tag":"same"}]}}`} {
		if _, err := decodeRouteTemplate(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
