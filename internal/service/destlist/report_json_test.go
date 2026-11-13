package destlist

import (
	"encoding/json"
	"strings"
	"testing"
)

// The page reads samples and warnings as arrays; a clean list must not send null.
func TestParseReport_EncodesEmptyListsAsArrays(t *testing.T) {
	raw, err := json.Marshal(ParseReport{Accepted: 3})
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if strings.Contains(s, "null") || !strings.Contains(s, `"samples":[]`) || !strings.Contains(s, `"warnings":[]`) {
		t.Fatalf("report = %s", s)
	}
}
