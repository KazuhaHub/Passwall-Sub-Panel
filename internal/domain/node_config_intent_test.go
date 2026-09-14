package domain

import (
	"encoding/json"
	"testing"
)

func TestNodeConfigIntentKeepsNativeListenerWireShape(t *testing.T) {
	node := &Node{
		Enabled: true, InboundListen: "0.0.0.0", DesiredPort: 444,
		DesiredProtocol: "vless", InboundRemark: "native",
		InboundSettings: `{}`, StreamSettings: `{}`, Sniffing: `{}`, Allocate: `{}`,
		InboundExpiryTime: 123,
		// Observation and metadata must not alter the minted listener bytes.
		ObservedPort: 443, DisplayName: "display", LifetimeTotalBytes: 99,
	}
	raw, err := json.Marshal(node.ConfigIntent())
	if err != nil {
		t.Fatal(err)
	}
	const previousWire = `{"enabled":true,"listen":"0.0.0.0","port":444,"protocol":"vless","remark":"native","settings":"{}","stream_settings":"{}","sniffing":"{}","allocate":"{}","expiry_time":123}`
	if string(raw) != previousWire {
		t.Fatalf("native listener encoding changed: %s", raw)
	}
	var roundtrip NodeConfigIntent
	if err := json.Unmarshal(raw, &roundtrip); err != nil || roundtrip != node.ConfigIntent() {
		t.Fatalf("listener intent did not round trip: %v", err)
	}
}

func TestNilNodeConfigIntentIsSafe(t *testing.T) {
	var node *Node
	if node.ConfigIntent() != (NodeConfigIntent{}) {
		t.Fatal("nil node should not produce configuration intent")
	}
}
