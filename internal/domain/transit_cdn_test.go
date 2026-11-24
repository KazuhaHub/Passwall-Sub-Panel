package domain

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"
)

func certFor(t *testing.T, names ...string) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: names[0]}, DNSNames: names,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func cdnNode(proto, stream string) *Node {
	return &Node{ID: 7, DisplayName: "Tokyo WS", DesiredProtocol: proto, DesiredPort: 443, StreamSettings: stream}
}

func tlsStream(network, extra string) string {
	return `{"network":"` + network + `","security":"tls","tlsSettings":{"serverName":"cdn.example.com"` + extra + `}}`
}

func TestCDNLandingProblem_Matrix(t *testing.T) {
	cases := []struct {
		name   string
		node   *Node
		refuse string // substring of the refusal, "" accepts
	}{
		{"vless ws tls", cdnNode("vless", tlsStream("ws", "")), ""},
		{"vmess grpc tls", cdnNode("vmess", tlsStream("grpc", "")), ""},
		{"trojan httpupgrade tls", cdnNode("trojan", tlsStream("httpupgrade", "")), ""},
		{"reality", cdnNode("vless", `{"network":"tcp","security":"reality"}`), "REALITY"},
		{"raw tcp tls", cdnNode("vless", tlsStream("tcp", "")), "raw TCP"},
		{"vless ws plain", cdnNode("vless", `{"network":"ws","security":"none"}`), ""},
		{"vmess httpupgrade plain", cdnNode("vmess", `{"network":"httpupgrade","security":"none"}`), ""},
		{"trojan ws plain", cdnNode("trojan", `{"network":"ws","security":"none"}`), "Trojan without TLS"},
		{"vless grpc plain", cdnNode("vless", `{"network":"grpc","security":"none"}`), "gRPC without TLS"},
		{"unknown security", cdnNode("vless", `{"network":"ws","security":"xtls"}`), "unsupported security"},
		{"xhttp", cdnNode("vless", tlsStream("xhttp", "")), "XHTTP"},
		{"kcp", cdnNode("vless", tlsStream("kcp", "")), "kcp"},
		{"hysteria2", cdnNode("hysteria2", tlsStream("tcp", "")), "Hysteria2"},
		{"tuic", cdnNode("tuic", tlsStream("tcp", "")), "TUIC"},
		{"shadowsocks", cdnNode("shadowsocks", `{}`), "Shadowsocks"},
		{"ss2022", cdnNode("2022-blake3-aes-128-gcm", `{}`), ""},
		{"unreadable", cdnNode("vless", ""), "no readable transport"},
		{"vision flow", func() *Node { n := cdnNode("vless", tlsStream("ws", "")); n.Flow = "xtls-rprx-vision"; return n }(), "xtls-rprx-vision"},
	}
	for _, c := range cases {
		got := CDNLandingProblem(c.node)
		if c.name == "ss2022" {
			if got == "" {
				t.Errorf("ss2022: want a refusal, got none")
			}
			continue
		}
		if c.refuse == "" && got != "" || c.refuse != "" && !strings.Contains(got, c.refuse) {
			t.Errorf("%s: problem = %q, want %q", c.name, got, c.refuse)
		}
		if c.refuse != "" && !strings.Contains(got, `"Tokyo WS"`) {
			t.Errorf("%s: refusal must name the node: %q", c.name, got)
		}
		if c.refuse != "" && c.name != "vision flow" && c.name != "unreadable" && c.name != "vless grpc plain" && c.name != "trojan ws plain" && c.name != "unknown security" && c.name != "xhttp" && !strings.Contains(got, "TLS + WebSocket") {
			t.Errorf("%s: refusal should suggest a TLS + WS/gRPC node: %q", c.name, got)
		}
	}
	if got := CDNLandingProblem(nil); got == "" {
		t.Errorf("nil landing must be refused")
	}
}

func TestNormalizeCDNDomain(t *testing.T) {
	good := map[string]string{"CDN.Example.com": "cdn.example.com", " a.b.co. ": "a.b.co", "xn--fiq.example.org": "xn--fiq.example.org"}
	for in, want := range good {
		if got, err := NormalizeCDNDomain(in); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "  ", "example", "https://a.example.com", "a.example.com:443", "a.example.com/x", "*.example.com", "1.2.3.4", "a b.example.com", "-a.example.com", "例子.com", "a..b.com"} {
		if got, err := NormalizeCDNDomain(bad); err == nil {
			t.Errorf("%q should be refused, got %q", bad, got)
		}
	}
}

func TestSettleCDNLines_FillsAndValidates(t *testing.T) {
	lines := []RelayLine{{ID: "a", Mode: RelayModeCDN, Domain: "CDN.example.com", Enabled: true}}
	if err := SettleCDNLines(nil, lines); err != nil {
		t.Fatal(err)
	}
	l := lines[0]
	if l.Domain != "cdn.example.com" || l.Address != "cdn.example.com" || l.Port != 443 || l.SNI != "cdn.example.com" || l.Host != "cdn.example.com" {
		t.Fatalf("defaults not filled: %+v", l)
	}
	// Given values are kept literally.
	lines = []RelayLine{{ID: "b", Mode: RelayModeCDN, Domain: "cdn.example.com", Address: "104.16.1.1", Port: 8443, SNI: "sni.example.com", Host: "host.example.com"}}
	if err := SettleCDNLines(nil, lines); err != nil {
		t.Fatal(err)
	}
	l = lines[0]
	if l.Address != "104.16.1.1" || l.Port != 8443 || l.SNI != "sni.example.com" || l.Host != "host.example.com" {
		t.Fatalf("given values changed: %+v", l)
	}
	bad := []struct {
		name string
		line RelayLine
	}{
		{"no domain", RelayLine{ID: "x", Mode: RelayModeCDN}},
		{"ip domain", RelayLine{ID: "x", Mode: RelayModeCDN, Domain: "1.2.3.4"}},
		{"address with port", RelayLine{ID: "x", Mode: RelayModeCDN, Domain: "cdn.example.com", Address: "1.2.3.4:443"}},
		{"port range", RelayLine{ID: "x", Mode: RelayModeCDN, Domain: "cdn.example.com", Port: 70000}},
		{"entry server", RelayLine{ID: "x", Mode: RelayModeCDN, Domain: "cdn.example.com", EntryPanelID: 3}},
		{"bad sni", RelayLine{ID: "x", Mode: RelayModeCDN, Domain: "cdn.example.com", SNI: "a b"}},
		{"domain on forward", RelayLine{ID: "x", Domain: "cdn.example.com", Address: "a"}},
	}
	for _, c := range bad {
		err := SettleCDNLines(nil, []RelayLine{c.line})
		if !errors.Is(err, ErrValidation) {
			t.Errorf("%s: want ErrValidation, got %v", c.name, err)
		}
	}
}

func TestSettleCDNLines_IPv6(t *testing.T) {
	for _, address := range []string{"2001:db8::1", "[2001:db8::1]"} {
		lines := []RelayLine{{Mode: RelayModeCDN, Domain: "cdn.example.com", Address: address}}
		if err := SettleCDNLines(nil, lines); err != nil || lines[0].Address != "2001:db8::1" {
			t.Errorf("address %q settled to %q, err = %v", address, lines[0].Address, err)
		}
	}
}

func TestSettleCDNLines_KindNeverChanges(t *testing.T) {
	prev := []RelayLine{{ID: "a", Mode: RelayModeCDN, Domain: "cdn.example.com", Address: "1.1.1.1", Port: 443}, {ID: "f", Address: "x", Port: 1}}
	// CDN -> forward and forward -> CDN are refused.
	if err := SettleCDNLines(prev, []RelayLine{{ID: "a", Address: "x", Port: 1}}); !errors.Is(err, ErrValidation) {
		t.Errorf("cdn to forward: %v", err)
	}
	if err := SettleCDNLines(prev, []RelayLine{{ID: "f", Mode: RelayModeCDN, Domain: "cdn.example.com"}}); !errors.Is(err, ErrValidation) {
		t.Errorf("forward to cdn: %v", err)
	}
	// An old client that omits the mode and the domain keeps both.
	lines := []RelayLine{{ID: "a", ModeUnresolved: true, Address: "1.1.1.1", Port: 443, SNI: "cdn.example.com", Host: "cdn.example.com"}}
	if err := SettleCDNLines(prev, lines); err != nil || lines[0].Mode != RelayModeCDN || lines[0].Domain != "cdn.example.com" {
		t.Errorf("omitted mode: %+v %v", lines[0], err)
	}
}

func TestValidateCDNRelays_NamesNodeAndLine(t *testing.T) {
	line := RelayLine{ID: "a", Name: "CF-1", Mode: RelayModeCDN, Domain: "cdn.example.com"}
	err := ValidateCDNRelays(cdnNode("vless", `{"network":"tcp","security":"reality"}`), []RelayLine{line})
	if !errors.Is(err, ErrValidation) || !strings.Contains(err.Error(), `"Tokyo WS"`) || !strings.Contains(err.Error(), "CF-1") {
		t.Fatalf("refusal = %v", err)
	}
	if err := ValidateCDNRelays(cdnNode("vless", tlsStream("ws", "")), []RelayLine{line}); err != nil {
		t.Fatal(err)
	}
	// A landing with no CDN lines is never judged.
	if err := ValidateCDNRelays(cdnNode("vless", `{"network":"tcp","security":"reality"}`), []RelayLine{{ID: "f", Address: "x"}}); err != nil {
		t.Fatal(err)
	}
}

func codes(issues []CDNIssue) string {
	var c []string
	for _, i := range issues {
		c = append(c, i.Code)
	}
	return strings.Join(c, ",")
}

func TestCDNWarnings_Matrix(t *testing.T) {
	line := RelayLine{ID: "a", Mode: RelayModeCDN, Domain: "cdn.example.com", Address: "1.1.1.1", Port: 443, SNI: "cdn.example.com", Host: "cdn.example.com"}
	stream := func(extra string) string { return tlsStream("ws", extra) }
	cert := func(names ...string) string {
		b, _ := json.Marshal(certFor(t, names...))
		return `,"certificates":[{"certificate":[` + string(b) + `]}]`
	}
	cases := []struct {
		name string
		n    *Node
		l    func(RelayLine) RelayLine
		want string
	}{
		{"covered cert, cf ports", cdnNode("vless", stream(cert("cdn.example.com"))), nil, ""},
		{"wildcard cert", cdnNode("vless", stream(cert("*.example.com"))), nil, ""},
		{"other cert", cdnNode("vless", stream(cert("other.example.org"))), nil, CDNIssueCertNotCovering},
		{"cert as file", cdnNode("vless", stream(`,"certificates":[{"certificateFile":"/a.pem"}]`)), nil, CDNIssueCertUnverifiable},
		{"server name differs", cdnNode("vless", `{"network":"ws","security":"tls","tlsSettings":{"serverName":"origin.example.org"}}`), nil, CDNIssueServerName + "," + CDNIssueCertUnverifiable},
		{"origin port not cf", func() *Node { n := cdnNode("vless", stream(cert("cdn.example.com"))); n.DesiredPort = 10443; return n }(), func(l RelayLine) RelayLine { l.Port = 10443; return l }, CDNIssueOriginPort + "," + CDNIssueLinePort},
		{"line port not cf only", cdnNode("vless", stream(cert("cdn.example.com"))), func(l RelayLine) RelayLine { l.Port = 9000; return l }, CDNIssueLinePort + "," + CDNIssuePortDiffers},
		{"cf alt port differs", cdnNode("vless", stream(cert("cdn.example.com"))), func(l RelayLine) RelayLine { l.Port = 2053; return l }, CDNIssuePortDiffers},
		{"sni and host differ", cdnNode("vless", stream(cert("cdn.example.com"))), func(l RelayLine) RelayLine { l.SNI, l.Host = "a.example.net", "b.example.net"; return l }, CDNIssueSNIDiffers + "," + CDNIssueHostDiffers},
		{"insecure ignored", cdnNode("vless", stream(cert("cdn.example.com")+`,"allowInsecure":true`)), nil, CDNIssueInsecureIgnored},
		{"not capable", cdnNode("vless", `{"network":"tcp","security":"reality"}`), nil, CDNIssueLandingNotCapable},
	}
	for _, c := range cases {
		l := line
		if c.l != nil {
			l = c.l(l)
		}
		if got := codes(CDNWarnings(c.n, l)); got != c.want {
			t.Errorf("%s: warnings = %q, want %q", c.name, got, c.want)
		}
	}
	// Only a CDN line has any.
	if got := CDNWarnings(cdnNode("vless", stream("")), RelayLine{Address: "x"}); got != nil {
		t.Errorf("forward line: %v", got)
	}
}

func TestCDNWarnings_PlainOriginAndClientTLS(t *testing.T) {
	plainNode := func(proto string, port int) *Node {
		n := cdnNode(proto, `{"network":"ws","security":"none"}`)
		n.DesiredPort = port
		return n
	}
	on, off := true, false
	line := func(port int, tls *bool) RelayLine {
		return RelayLine{ID: "a", Mode: RelayModeCDN, Domain: "cdn.example.com", Address: "1.1.1.1", Port: port, SNI: "cdn.example.com", Host: "cdn.example.com", ClientTLS: tls}
	}
	for name, tc := range map[string]struct {
		n    *Node
		l    RelayLine
		want string
	}{
		"flexible: https client, http origin": {plainNode("vless", 8080), line(443, &on), CDNIssueOriginPlaintext + "," + CDNIssuePortDiffers},
		"plain end to end on http ports":      {plainNode("vmess", 8080), line(8080, &off), CDNIssueOriginPlaintext + "," + CDNIssueClientPlaintext},
		"http origin on a https port":         {plainNode("vless", 443), line(443, &on), CDNIssueOriginPlaintext + "," + CDNIssueOriginPort},
		"http client on a https port":         {plainNode("vless", 8080), line(443, &off), CDNIssueOriginPlaintext + "," + CDNIssueClientPlaintext + "," + CDNIssueLinePort + "," + CDNIssuePortDiffers},
		"tls client on an http port":          {plainNode("vless", 8080), line(8080, &on), CDNIssueOriginPlaintext + "," + CDNIssueLinePort},
	} {
		if got := codes(CDNWarnings(tc.n, tc.l)); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
	if w := CDNWarnings(plainNode("vless", 8080), line(443, &on)); w[0].Params["protocol"] != "VLESS" || !w[0].Warn {
		t.Errorf("origin_plaintext = %+v", w[0])
	}
	// The ports in the message are the list of the kind that applies.
	w := CDNWarnings(plainNode("vless", 9000), line(8080, &off))
	var origin CDNIssue
	for _, i := range w {
		if i.Code == CDNIssueOriginPort {
			origin = i
		}
	}
	if origin.Params["kind"] != "HTTP" || origin.Params["ports"] != "80, 8080, 8880, 2052, 2082, 2086, 2095" {
		t.Errorf("origin_port = %+v", origin)
	}
}

func TestCDNWarnings_OriginHostAndFaultParams(t *testing.T) {
	n := cdnNode("vless", `{"network":"ws","security":"tls","tlsSettings":{"serverName":"cdn.example.com"},"wsSettings":{"host":"origin.example.com"}}`)
	line := RelayLine{ID: "a", Mode: RelayModeCDN, Domain: "cdn.example.com", Address: "1.1.1.1", Port: 443, SNI: "cdn.example.com", Host: "cdn.example.com"}
	got := CDNWarnings(n, line)
	found := false
	for _, i := range got {
		if i.Code == CDNIssueOriginHost && i.Warn && i.Params["origin_host"] == "origin.example.com" {
			found = true
		}
	}
	if !found {
		t.Errorf("wsSettings.host that differs from the line's Host must warn: %v", codes(got))
	}
	// The same Host is quiet; HTTPUpgrade host is checked as well.
	line.Host = "ORIGIN.example.com"
	for _, i := range CDNWarnings(n, line) {
		if i.Code == CDNIssueOriginHost {
			t.Errorf("same host warned: %+v", i)
		}
	}
	hu := cdnNode("vless", `{"network":"httpupgrade","security":"tls","httpupgradeSettings":{"host":"h.example.com"}}`)
	line.Host = "cdn.example.com"
	if got := codes(CDNWarnings(hu, line)); !strings.Contains(got, CDNIssueOriginHost) {
		t.Errorf("httpupgrade host: %s", got)
	}
	// The not-capable issue carries a code and params for the editor to localize.
	bad := CDNWarnings(cdnNode("hysteria2", `{}`), line)[0]
	if bad.Code != CDNIssueLandingNotCapable || bad.Params["reason_code"] != CDNFaultProtocol || bad.Params["protocol"] != "Hysteria2" || bad.Params["node"] != "Tokyo WS" {
		t.Errorf("not capable = %+v", bad)
	}
}

func TestRelayLineLabelPrefersTheDomainOverTheOptimisedIP(t *testing.T) {
	cdn := RelayLine{Mode: RelayModeCDN, Domain: "cdn.example.com", Address: "104.16.1.1"}
	if cdn.Label() != "cdn.example.com" {
		t.Errorf("label = %q", cdn.Label())
	}
	cdn.Name = "CF"
	if cdn.Label() != "CF" || (RelayLine{Address: "gz.relay.cn"}).Label() != "gz.relay.cn" {
		t.Error("name first, address for other lines")
	}
}

func TestCDNWarnings_GRPC(t *testing.T) {
	n := cdnNode("vless", `{"network":"grpc","security":"tls","grpcSettings":{"authority":"outbound.example.com"}}`)
	n.DesiredPort = 443
	line := RelayLine{Mode: RelayModeCDN, Domain: "cdn.example.com", Port: 443, SNI: "cdn.example.com", Host: "cdn.example.com"}
	if got := codes(CDNWarnings(n, line)); strings.Contains(got, CDNIssueOriginHost) ||
		strings.Contains(got, CDNIssueCloudflareGRPC) || strings.Contains(got, CDNIssueGRPCAuthority) {
		t.Fatalf("unexpected gRPC warnings: %s", got)
	}
	line.Port = 8443
	line.Host = "authority.example.com"
	if got := codes(CDNWarnings(n, line)); !strings.Contains(got, CDNIssueCloudflareGRPC) ||
		!strings.Contains(got, CDNIssueGRPCAuthority) {
		t.Errorf("missing gRPC warnings: %s", got)
	}
}

// Xray reads wsSettings.host first and the Host header only when it is empty,
// and matches the header key case-insensitively.
func TestCDNWarnings_OriginHostPrecedenceAndCase(t *testing.T) {
	line := RelayLine{ID: "a", Mode: RelayModeCDN, Domain: "cdn.example.com", Address: "1.1.1.1", Port: 443, SNI: "cdn.example.com"}
	ws := func(extra string) *Node {
		return cdnNode("vless", `{"network":"ws","security":"tls","tlsSettings":{"serverName":"cdn.example.com"},"wsSettings":{`+extra+`}}`)
	}
	warns := func(n *Node, host string) bool {
		l := line
		l.Host = host
		return strings.Contains(codes(CDNWarnings(n, l)), CDNIssueOriginHost)
	}
	both := ws(`"host":"a.com","headers":{"Host":"b.com"}`)
	if warns(both, "a.com") || !warns(both, "b.com") {
		t.Errorf("host must win over headers.Host")
	}
	lower := ws(`"headers":{"host":"b.com"}`)
	if warns(lower, "b.com") || !warns(lower, "a.com") {
		t.Errorf("a lowercase host header must be read")
	}
}

func TestValidateCDNRelays_ClientTLSOff(t *testing.T) {
	off := false
	line := RelayLine{ID: "a", Name: "CF-1", Mode: RelayModeCDN, Domain: "cdn.example.com", ClientTLS: &off}
	for code, n := range map[string]*Node{
		CodeCDNClientTLSGRPC:   cdnNode("vless", tlsStream("grpc", "")),
		CodeCDNClientTLSTrojan: cdnNode("trojan", tlsStream("ws", "")),
	} {
		err := ValidateCDNRelays(n, []RelayLine{line})
		var coded *CodedValidationError
		if !errors.Is(err, ErrValidation) || !errors.As(err, &coded) || coded.Code != code {
			t.Errorf("%s: refusal = %v", code, err)
		}
	}
	if err := ValidateCDNRelays(cdnNode("vless", tlsStream("ws", "")), []RelayLine{line}); err != nil {
		t.Errorf("ws with client TLS off must be allowed: %v", err)
	}
}

func TestCDNWarnings_HTTPUpgradeHostHeader(t *testing.T) {
	line := RelayLine{ID: "a", Mode: RelayModeCDN, Domain: "cdn.example.com", Address: "1.1.1.1", Port: 443, SNI: "cdn.example.com", Host: "cdn.example.com"}
	bad := cdnNode("vless", `{"network":"httpupgrade","security":"tls","tlsSettings":{"serverName":"cdn.example.com"},"httpupgradeSettings":{"headers":{"host":"cdn.example.com"}}}`)
	if got := codes(CDNWarnings(bad, line)); !strings.Contains(got, CDNIssueHTTPUpgradeHeader) {
		t.Errorf("a Host inside HTTPUpgrade headers must warn: %s", got)
	}
	ok := cdnNode("vless", `{"network":"httpupgrade","security":"tls","tlsSettings":{"serverName":"cdn.example.com"},"httpupgradeSettings":{"host":"cdn.example.com"}}`)
	if got := codes(CDNWarnings(ok, line)); strings.Contains(got, CDNIssueHTTPUpgradeHeader) {
		t.Errorf("the host field must not warn: %s", got)
	}
}
