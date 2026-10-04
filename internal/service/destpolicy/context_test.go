package destpolicy

import (
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-protocol/protocol"
)

func contextConfig() protocol.ConfigBody {
	return protocol.ConfigBody{Core: protocol.CoreSelection{Engine: "xray", Version: "26.6.27"}, Listeners: []protocol.Listener{{Key: "lis_1", Config: protocol.RawConfig(`{"enabled":true,"port":443,"sniffing":"{\"enabled\":true,\"metadataOnly\":true}"}`)}}}
}

func TestPolicyContextTracksOnlyRetryInputs(t *testing.T) {
	caps := []string{protocol.CapabilityDestinationPolicy, "audit.hits.v1", "audit.usage.v1"}
	base := contextConfig()
	want, err := PolicyContext(7, caps, base)
	if err != nil || len(want) != 64 {
		t.Fatalf("missing context: %q / %v", want, err)
	}
	for _, input := range []string{"generation", "policy", "hits", "usage", "engine", "version", "sniffing", "listener-enable"} {
		t.Run(input, func(t *testing.T) {
			generation, current := int64(7), contextConfig()
			currentCaps := append([]string(nil), caps...)
			switch input {
			case "generation":
				generation++
			case "policy":
				currentCaps = currentCaps[1:]
			case "hits":
				currentCaps = []string{protocol.CapabilityDestinationPolicy, "audit.usage.v1"}
			case "usage":
				currentCaps = currentCaps[:2]
			case "engine":
				current.Core.Engine = "sing-box"
			case "version":
				current.Core.Version = "26.9.9"
			case "sniffing":
				current.Listeners[0].Config = protocol.RawConfig(`{"enabled":true,"sniffing":"{\"enabled\":false}"}`)
			case "listener-enable":
				current.Listeners[0].Config = protocol.RawConfig(`{"enabled":false}`)
			}
			got, err := PolicyContext(generation, currentCaps, current)
			if err != nil || got == want {
				t.Fatalf("retry input ignored: %q / %v", got, err)
			}
		})
	}
}

func TestPolicyContextIgnoresTasksPolicyAndUnrelatedListenerFields(t *testing.T) {
	caps := []string{protocol.CapabilityDestinationPolicy, "audit.hits.v1"}
	want, err := PolicyContext(7, caps, contextConfig())
	if err != nil {
		t.Fatal(err)
	}
	base := contextConfig()
	base.Policy = &protocol.DestinationPolicy{Rules: []protocol.DestinationRule{{ID: "p1", Action: protocol.RuleBlock, Ports: "443"}}}
	base.Listeners[0].Config = protocol.RawConfig(`{"remark":"changed","port":8443,"sniffing":"{ \"metadataOnly\" : true, \"enabled\" : true }","enabled":true}`)
	base.Listeners = append(base.Listeners, protocol.Listener{Key: "lis_2", Config: protocol.RawConfig(`{"enabled":false,"sniffing":"{\"enabled\":true}"}`)})
	got, err := PolicyContext(7, []string{"task.exec.v1", "audit.hits.v1", protocol.CapabilityDestinationPolicy}, base)
	if err != nil || got != want {
		t.Fatalf("irrelevant input unlocked retry: %q != %q / %v", got, want, err)
	}
	base.Core.Engine = ""
	got, err = PolicyContext(7, caps, base)
	if err != nil || got != want {
		t.Fatalf("default engine differs: %q / %v", got, err)
	}
}

func TestPolicyContextCanonicalListenerOrderAndMalformedConfig(t *testing.T) {
	base := contextConfig()
	base.Listeners = append(base.Listeners, protocol.Listener{Key: "lis_2", Config: protocol.RawConfig(`{"enabled":true}`)})
	want, err := PolicyContext(1, nil, base)
	if err != nil {
		t.Fatal(err)
	}
	base.Listeners[0], base.Listeners[1] = base.Listeners[1], base.Listeners[0]
	got, err := PolicyContext(1, nil, base)
	if err != nil || got != want {
		t.Fatalf("listener order changed context: %q / %v", got, err)
	}
	base.Listeners[0].Config = protocol.RawConfig(`{"enabled":"wrong"}`)
	got, err = PolicyContext(1, nil, base)
	if err != nil || len(got) != 64 || got == want || strings.Trim(got, "0123456789abcdef") != "" {
		t.Fatalf("malformed listener became policy failure: %q / %v", got, err)
	}
}
