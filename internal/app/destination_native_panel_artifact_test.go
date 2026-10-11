package app

import (
	"encoding/json"
	"reflect"
	"testing"
)

// The released compiler omitted log.access; the candidate's reviewed N0
// compiler explicitly emits "none". Permit only that fixed difference, then
// compare the entire artifact, including listeners, routing and outbounds.
// Neither artifact file is rewritten, and policy remnants must still fail.
func nativePanelPolicyFreeArtifactMatches(released, candidate []byte) bool {
	var expected, actual map[string]any
	if json.Unmarshal(released, &expected) != nil || json.Unmarshal(candidate, &actual) != nil {
		return false
	}
	oldLog, ok := expected["log"].(map[string]any)
	if !ok {
		return false
	}
	newLog, ok := actual["log"].(map[string]any)
	if !ok || newLog["access"] != "none" {
		return false
	}
	if _, exists := oldLog["access"]; exists {
		return false
	}
	oldLog["access"] = "none"
	return reflect.DeepEqual(expected, actual)
}

func TestNativePanelPolicyFreeArtifactAllowsOnlyReviewedN0LoggingChange(t *testing.T) {
	const released = `{"log":{"loglevel":"warning"},"inbounds":[{"port":12345}],"outbounds":[{"tag":"direct"}]}`
	const candidate = `{"log":{"loglevel":"warning","access":"none"},"inbounds":[{"port":12345}],"outbounds":[{"tag":"direct"}]}`
	if !nativePanelPolicyFreeArtifactMatches([]byte(released), []byte(candidate)) {
		t.Fatal("reviewed default logging change mistaken for policy execution")
	}
	for _, input := range []string{
		released, `null`, `{}`, `private invalid`,
		`{"log":{"loglevel":"warning","access":""},"inbounds":[{"port":12345}],"outbounds":[{"tag":"direct"}]}`,
		`{"log":{"loglevel":"warning","access":"access.log"},"inbounds":[{"port":12345}],"outbounds":[{"tag":"direct"}]}`,
		`{"log":{"loglevel":"debug","access":"none"},"inbounds":[{"port":12345}],"outbounds":[{"tag":"direct"}]}`,
		`{"log":{"loglevel":"warning","access":"none"},"inbounds":[{"port":12346}],"outbounds":[{"tag":"direct"}]}`,
		`{"log":{"loglevel":"warning","access":"none"},"inbounds":[{"port":12345}],"outbounds":[{"tag":"direct"},{"tag":"psp-deny-p1"}]}`,
		`{"log":{"loglevel":"warning","access":"none"},"inbounds":[{"port":12345}],"outbounds":[{"tag":"direct"}],"routing":{"rules":[{"port":"443","outboundTag":"blocked"}]}}`,
	} {
		if nativePanelPolicyFreeArtifactMatches([]byte(released), []byte(input)) {
			t.Fatal("artifact gate accepted a changed listener, logging, route or outbound")
		}
	}
	if nativePanelPolicyFreeArtifactMatches([]byte(candidate), []byte(candidate)) || nativePanelPolicyFreeArtifactMatches([]byte(`private invalid`), []byte(candidate)) {
		t.Fatal("artifact gate accepted an unreviewed released baseline")
	}
}
