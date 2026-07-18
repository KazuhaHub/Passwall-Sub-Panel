package nodespec

import (
	"errors"
	"reflect"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func TestDecodeRealityWebSocket(t *testing.T) {
	spec, err := Decode(ports.InboundSpec{
		Remark: "edge", Enable: true, Listen: "::", Port: 443, Protocol: "vless",
		Settings: `{"clients":[],"decryption":"none"}`,
		StreamSettings: `{
			"network":"ws","security":"reality",
			"wsSettings":{"path":"/ws","headers":{"Host":"cdn.example.com"}},
			"realitySettings":{"dest":"origin.example.com:8443","serverNames":["edge.example.com"],"privateKey":"private","shortIds":["a1"],"maxTimediff":250,"settings":{"publicKey":"public","fingerprint":"chrome"}}
		}`,
		Sniffing: `{"enabled":true,"destOverride":["http","tls"],"metadataOnly":true}`,
	})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if spec.Transport.Type != "ws" || spec.Transport.Path != "/ws" || spec.Transport.Host != "cdn.example.com" {
		t.Fatalf("transport = %#v", spec.Transport)
	}
	if spec.Security.Mode != "reality" || spec.Security.Reality.HandshakeServer != "origin.example.com" || spec.Security.Reality.HandshakePort != 8443 {
		t.Fatalf("reality target = %#v", spec.Security.Reality)
	}
	if spec.Security.Reality.PrivateKey != "private" || spec.Security.Reality.PublicKey != "public" || spec.Security.Reality.Fingerprint != "chrome" {
		t.Fatalf("reality keys = %#v", spec.Security.Reality)
	}
	if !reflect.DeepEqual(spec.Security.Reality.ShortIDs, []string{"a1"}) ||
		!reflect.DeepEqual(spec.Sniffing.DestinationOverride, []string{"http", "tls"}) ||
		!spec.Sniffing.Enabled || !spec.Sniffing.MetadataOnly {
		t.Fatalf("decoded spec = %#v", spec)
	}
}

func TestDecodeShadowsocksAndHysteria2(t *testing.T) {
	ss, err := Decode(ports.InboundSpec{
		Port: 8388, Protocol: "shadowsocks",
		Settings: `{"method":"2022-blake3-aes-256-gcm","password":"secret","network":"tcp,udp"}`,
	})
	if err != nil {
		t.Fatalf("Decode shadowsocks: %v", err)
	}
	if ss.Shadowsocks.Method != "2022-blake3-aes-256-gcm" || ss.Shadowsocks.Password != "secret" || ss.Shadowsocks.Network != "tcp,udp" {
		t.Fatalf("shadowsocks = %#v", ss.Shadowsocks)
	}

	hy2, err := Decode(ports.InboundSpec{
		Port: 8443, Protocol: "hysteria2",
		StreamSettings: `{
			"network":"hysteria","security":"tls",
			"hysteriaSettings":{"udpIdleTimeout":75,"masquerade":{"type":"file","dir":"/srv/www"}},
			"finalmask":{"udp":[{"type":"salamander","settings":{"password":"obfs"}}]}
		}`,
	})
	if err != nil {
		t.Fatalf("Decode hysteria2: %v", err)
	}
	if hy2.Transport.Type != "tcp" || hy2.Hysteria2.ObfsPassword != "obfs" || hy2.Hysteria2.UDPIdleTimeoutSeconds != 75 || hy2.Hysteria2.MasqueradeData != "/srv/www" {
		t.Fatalf("hysteria2 = %#v", hy2.Hysteria2)
	}
}

func TestDecodeSocketAcceptProxyProtocol(t *testing.T) {
	spec, err := Decode(ports.InboundSpec{
		Port: 443, Protocol: "vless",
		StreamSettings: `{"network":"tcp","sockopt":{"acceptProxyProtocol":true}}`,
	})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !spec.Socket.AcceptProxyProtocol {
		t.Fatalf("socket options = %#v", spec.Socket)
	}
}

func TestDecodeRejectsInvalidInput(t *testing.T) {
	for _, input := range []ports.InboundSpec{
		{Port: 0, Protocol: "vless"},
		{Port: 443, Protocol: "vless", Settings: `{`},
		{Port: 443, Protocol: "vless", StreamSettings: `[]`},
	} {
		if _, err := Decode(input); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("Decode(%#v) error = %v", input, err)
		}
	}
}
