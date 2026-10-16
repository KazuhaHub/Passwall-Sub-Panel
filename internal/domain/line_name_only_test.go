package domain

import (
	"encoding/json"
	"testing"
)

func TestLegacyLinesDefaultToCombinedNames(t *testing.T) {
	var relay RelayLine
	if err := json.Unmarshal([]byte(`{"Name":"relay","Address":"relay.example","Enabled":true}`), &relay); err != nil {
		t.Fatal(err)
	}
	var route VlessRouteLine
	if err := json.Unmarshal([]byte(`{"id":"route","name":"route","outbound_tag":"direct","enabled":true}`), &route); err != nil {
		t.Fatal(err)
	}
	if relay.OnlyLineName || route.OnlyLineName || route.EntryRelayID != "" {
		t.Fatal("legacy data unexpectedly enabled line-only names")
	}
}
