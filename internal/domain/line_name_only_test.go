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
	if relay.OnlyLineName {
		t.Fatal("legacy data unexpectedly enabled line-only names")
	}
}
