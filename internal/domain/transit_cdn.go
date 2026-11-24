package domain

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
)

// CDN lines (CDN（如 Cloudflare）).
//
// A CDN line is a relay line with no entry server and no forward: clients
// connect to a CDN, which reaches the landing node's own listener as its
// origin. PSP cannot see the CDN, so the line is published whenever its landing
// is, and nothing is probed. What PSP does own is the line's shape: the CDN
// domain, the address clients dial (an optimised IP, or the domain itself), the
// port, and the SNI and Host the client presents (the CDN routes by them). They
// are the line's own Address, Port, SNI and Host, stored as the administrator
// sees them; Domain is the one name they are filled from.

// RelayModeCDN marks a CDN line.
const RelayModeCDN = "cdn"

// CDN reports whether the line goes through a CDN.
func (r RelayLine) CDN() bool { return r.Mode == RelayModeCDN }

// CDNDefaultPort is the port a CDN line is made with when none is given.
const CDNDefaultPort = 443

// CloudflareHTTPSPorts are the ports Cloudflare proxies for HTTPS. Another CDN
// may allow others, so a port outside the list is a warning, not a refusal.
var CloudflareHTTPSPorts = []int{443, 2053, 2083, 2087, 2096, 8443}

// CloudflareHTTPPorts are the ports Cloudflare proxies for plain HTTP (a
// Flexible origin, or a client that does not use TLS to the CDN).
var CloudflareHTTPPorts = []int{80, 8080, 8880, 2052, 2082, 2086, 2095}

// CDNDefaultClientTLS is whether clients use TLS to the CDN by default for a
// line on this port: on, unless it is one of the HTTP-only ports.
func CDNDefaultClientTLS(port int) bool {
	return !slices.Contains(CloudflareHTTPPorts, port)
}

// ClientUsesTLS is whether a client of the line uses TLS to the CDN, given
// whether the landing itself uses TLS (the answer for a line that states none).
func (r RelayLine) ClientUsesTLS(originTLS bool) bool {
	if r.ClientTLS != nil {
		return *r.ClientTLS
	}
	return originTLS
}

func portList(ports []int) string {
	out := make([]string, len(ports))
	for i, p := range ports {
		out[i] = strconv.Itoa(p)
	}
	return strings.Join(out, ", ")
}

// CDN issue codes. Each is shown with an explanation in the editor; Params
// carry the values the sentence names.
const (
	CDNIssueCertNotCovering   = "cert_not_covering"       // an inline certificate on the landing does not cover the domain
	CDNIssueCertUnverifiable  = "cert_unverifiable"       // the landing's certificate cannot be read here (a file, or none)
	CDNIssueServerName        = "server_name_differs"     // the landing's TLS serverName is not the domain
	CDNIssueOriginPort        = "origin_port"             // the landing's port is not one Cloudflare proxies
	CDNIssueLinePort          = "line_port"               // the line's port is not one Cloudflare proxies
	CDNIssuePortDiffers       = "port_differs"            // the line's port is not the landing's port
	CDNIssueSNIDiffers        = "sni_differs"             // the SNI is not the domain
	CDNIssueHostDiffers       = "host_differs"            // the Host is not the domain
	CDNIssueInsecureIgnored   = "insecure_ignored"        // the landing allows insecure TLS; the line never does
	CDNIssueLandingNotCapable = "landing_not_capable"     // the landing cannot go through a CDN (also a save refusal)
	CDNIssueOriginPlaintext   = "origin_plaintext"        // the CDN reaches the landing without TLS
	CDNIssueClientPlaintext   = "client_plaintext"        // clients reach the CDN without TLS
	CDNIssueOriginHost        = "origin_host"             // the landing expects another Host than the line sends
	CDNIssueCloudflareGRPC    = "cloudflare_grpc"         // Cloudflare gRPC requires port 443 with TLS
	CDNIssueGRPCAuthority     = "grpc_authority"          // some clients use SNI as the authority
	CDNIssueHTTPUpgradeHeader = "httpupgrade_host_header" // Xray refuses an HTTPUpgrade Host set inside headers
)

// CDNIssue is a warning about a CDN line: it does not block saving.
type CDNIssue struct {
	Code string
	// Warn is true for something likely to stop the line working, false for a
	// note about a setting that looks deliberate.
	Warn   bool
	Params map[string]string
}

// cdnStream is the part of a landing's stream settings a CDN line depends on.
type cdnStream struct {
	Network     string `json:"network"`
	Security    string `json:"security"`
	TLSSettings *struct {
		ServerName    string `json:"serverName"`
		AllowInsecure bool   `json:"allowInsecure"`
		Certificates  []struct {
			Certificate     []string `json:"certificate"`
			CertificateFile string   `json:"certificateFile"`
		} `json:"certificates"`
	} `json:"tlsSettings"`
	WSSettings *struct {
		Host    string            `json:"host"`
		Headers map[string]string `json:"headers"`
	} `json:"wsSettings"`
	HTTPUpgradeSettings *struct {
		Host    string            `json:"host"`
		Headers map[string]string `json:"headers"`
	} `json:"httpupgradeSettings"`
}

func parseCDNStream(n *Node) (cdnStream, bool) {
	var s cdnStream
	if n == nil || strings.TrimSpace(n.StreamSettings) == "" {
		return s, false
	}
	if json.Unmarshal([]byte(n.StreamSettings), &s) != nil {
		return s, false
	}
	s.Network, s.Security = strings.ToLower(s.Network), strings.ToLower(s.Security)
	if s.Network == "" {
		s.Network = "tcp"
	}
	return s, true
}

// headerHostFold returns the Host header value, matching the key
// case-insensitively as Xray does.
func headerHostFold(h map[string]string) string {
	for k, v := range h {
		if strings.EqualFold(k, "host") && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// originHost is the HTTP Host the landing expects.
func (s cdnStream) originHost() string {
	first := func(vals ...string) string {
		for _, v := range vals {
			if strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
		return ""
	}
	switch s.Network {
	case "ws":
		if s.WSSettings != nil {
			// Xray reads wsSettings.host and falls back to the deprecated
			// Host header only when it is empty.
			return first(s.WSSettings.Host, headerHostFold(s.WSSettings.Headers))
		}
	case "httpupgrade":
		if s.HTTPUpgradeSettings != nil {
			return first(s.HTTPUpgradeSettings.Host, headerHostFold(s.HTTPUpgradeSettings.Headers))
		}
	}
	return ""
}

// CDN landing fault codes: what CDNLandingFault answers, each with the params
// its sentence names, so the editor can say it in the administrator's language.
const (
	CDNFaultUnknownNode = "unknown_node"
	CDNFaultProtocol    = "protocol"   // params: protocol
	CDNFaultUnreadable  = "unreadable" // the transport settings cannot be read
	CDNFaultReality     = "reality"
	CDNFaultNoTLSTrojan = "no_tls_trojan"
	CDNFaultNoTLSGRPC   = "no_tls_grpc"
	CDNFaultNoTLS       = "no_tls"
	CDNFaultRawTCP      = "raw_tcp"
	CDNFaultXHTTP       = "xhttp"
	CDNFaultTransport   = "transport" // params: network
	CDNFaultFlow        = "flow"
)

// CDNFault says why a node cannot be the origin behind a CDN.
type CDNFault struct {
	Code   string
	Params map[string]string
}

// CDNLandingFault is the reason a node cannot be the origin behind a CDN, or
// nil when it can. A CDN carries HTTP: only VLESS, VMess and Trojan over a
// WebSocket, gRPC or HTTPUpgrade transport qualify. REALITY borrows another
// site's handshake and raw TCP, Hysteria2, TUIC and Shadowsocks are not HTTP.
// TLS on the landing is needed except for VLESS and VMess over WebSocket or
// HTTPUpgrade, which a CDN can reach over plain HTTP (Cloudflare Flexible):
// that works but leaves the CDN-to-origin hop unprotected (see
// CDNWarnings). XHTTP is not carried because the subscription renderer cannot
// emit it for any client yet. n is judged as it is (or as it is being saved).
func CDNLandingFault(n *Node) *CDNFault {
	if n == nil {
		return &CDNFault{Code: CDNFaultUnknownNode}
	}
	proto := Protocol(strings.ToLower(n.DesiredProtocol))
	switch proto {
	case ProtoVLESS, ProtoVMess, ProtoTrojan:
	default:
		return &CDNFault{Code: CDNFaultProtocol, Params: map[string]string{"protocol": protocolLabel(n.DesiredProtocol)}}
	}
	s, ok := parseCDNStream(n)
	if !ok {
		return &CDNFault{Code: CDNFaultUnreadable}
	}
	if s.Security == "reality" {
		return &CDNFault{Code: CDNFaultReality}
	}
	switch s.Network {
	case "ws", "grpc", "httpupgrade":
	case "tcp":
		return &CDNFault{Code: CDNFaultRawTCP}
	case "xhttp", "splithttp":
		return &CDNFault{Code: CDNFaultXHTTP}
	default:
		return &CDNFault{Code: CDNFaultTransport, Params: map[string]string{"network": s.Network}}
	}
	switch s.Security {
	case "tls":
	case "", "none":
		switch {
		case proto == ProtoTrojan:
			return &CDNFault{Code: CDNFaultNoTLSTrojan}
		case s.Network == "grpc":
			return &CDNFault{Code: CDNFaultNoTLSGRPC}
		}
	default:
		return &CDNFault{Code: CDNFaultNoTLS}
	}
	if strings.EqualFold(strings.TrimSpace(n.Flow), "xtls-rprx-vision") {
		return &CDNFault{Code: CDNFaultFlow}
	}
	return nil
}

// CDNOriginTLS reports whether the landing's own listener uses TLS.
func CDNOriginTLS(n *Node) bool {
	s, ok := parseCDNStream(n)
	return ok && s.Security == "tls"
}

// CDNLandingProblem is CDNLandingFault as a sentence naming the node, or "".
func CDNLandingProblem(n *Node) string {
	f := CDNLandingFault(n)
	if f == nil {
		return ""
	}
	name := ""
	if n != nil {
		name = n.DisplayName
	}
	redo := " Create a TLS + WebSocket (or gRPC) node on the same landing server and put the CDN line on that node."
	switch f.Code {
	case CDNFaultUnknownNode:
		return "the landing node is unknown"
	case CDNFaultProtocol:
		return fmt.Sprintf("node %q uses %s, which cannot go through a CDN (only VLESS, VMess and Trojan over WebSocket, gRPC or HTTPUpgrade can).%s", name, f.Params["protocol"], redo)
	case CDNFaultUnreadable:
		return fmt.Sprintf("node %q has no readable transport settings yet, so PSP cannot tell that it can go through a CDN.", name)
	case CDNFaultReality:
		return fmt.Sprintf("node %q uses REALITY, which cannot go through a CDN (the CDN would have to terminate a TLS connection it cannot complete).%s", name, redo)
	case CDNFaultNoTLSTrojan:
		return fmt.Sprintf("node %q is Trojan without TLS: Trojan always needs TLS, so it cannot go through a CDN as it is.%s", name, redo)
	case CDNFaultNoTLSGRPC:
		return fmt.Sprintf("node %q carries gRPC without TLS, which a CDN cannot reach: gRPC needs TLS on the origin.%s", name, redo)
	case CDNFaultNoTLS:
		return fmt.Sprintf("node %q has an unsupported security setting, and a CDN needs TLS (or plain HTTP with VLESS/VMess over WebSocket or HTTPUpgrade).%s", name, redo)
	case CDNFaultRawTCP:
		return fmt.Sprintf("node %q uses raw TCP, which cannot go through a CDN (it carries HTTP only).%s", name, redo)
	case CDNFaultXHTTP:
		return fmt.Sprintf("node %q uses XHTTP, which PSP does not support on a CDN line yet: subscriptions cannot emit it.%s", name, redo)
	case CDNFaultTransport:
		return fmt.Sprintf("node %q uses the %q transport, which cannot go through a CDN (WebSocket, gRPC and HTTPUpgrade can).%s", name, f.Params["network"], redo)
	case CDNFaultFlow:
		return fmt.Sprintf("node %q has the xtls-rprx-vision flow, which needs raw TCP and cannot go through a CDN. Clear the flow or use another node.", name)
	}
	return f.Code
}

func protocolLabel(p string) string {
	switch Protocol(strings.ToLower(p)) {
	case ProtoSS, ProtoSS2022:
		return "Shadowsocks"
	case ProtoHysteria2:
		return "Hysteria2"
	case ProtoTUIC:
		return "TUIC"
	case ProtoAnyTLS:
		return "AnyTLS"
	case ProtoNaive:
		return "Naive"
	case "":
		return "an unknown protocol"
	}
	return strings.ToUpper(p)
}

// NormalizeCDNDomain returns a CDN domain in canonical form: lower-case, with
// no scheme, port, path, wildcard, trailing dot or IP literal.
func NormalizeCDNDomain(s string) (string, error) {
	d := strings.ToLower(strings.TrimSpace(s))
	d = strings.TrimSuffix(d, ".")
	if d == "" {
		return "", fmt.Errorf("the CDN domain is required")
	}
	if !validHostname(d) || !strings.Contains(d, ".") {
		return "", fmt.Errorf("%q is not a domain name: give a plain name such as cdn.example.com, with no scheme, port, path or wildcard (use the xn-- form for a non-ASCII name)", strings.TrimSpace(s))
	}
	if _, err := netip.ParseAddr(d); err == nil {
		return "", fmt.Errorf("%q is an IP address: a CDN line needs the domain proxied by the CDN (put the optimised IP in the address)", strings.TrimSpace(s))
	}
	return d, nil
}

// validHostname reports whether s is a syntactically valid DNS name (letters,
// digits, hyphen and underscore labels, each at most 63 bytes).
func validHostname(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
				return false
			}
		}
	}
	return true
}

// normalizeCDNDialAddress accepts a host or IP without a port. IPv6 brackets
// belong in the URI authority and are added when rendering the URI.
func normalizeCDNDialAddress(s string) (string, bool) {
	host := strings.TrimSpace(s)
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		address := host[1 : len(host)-1]
		ip, err := netip.ParseAddr(address)
		return address, err == nil && ip.Is6() && ip.Zone() == ""
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return host, ip.Zone() == ""
	}
	return host, validHostname(host)
}

// SettleCDNLines checks the CDN lines among lines and fills what they leave
// out from their domain, so what is stored is what the administrator sees: the
// address (the domain itself), the port (443), and the SNI and Host (the
// domain). Every filled value is an ordinary field of the line afterwards and is
// never derived again. It does not look at the landing: see
// ValidateCDNRelays. previous are the lines saved before (nil for a new node);
// a line's kind never changes once it is made.
func SettleCDNLines(previous, lines []RelayLine) error {
	was := make(map[string]RelayLine, len(previous))
	for _, p := range previous {
		was[p.ID] = p
	}
	for i := range lines {
		line := &lines[i]
		if prev, known := was[line.ID]; known && line.ID != "" {
			if line.ModeUnresolved {
				line.Mode = prev.Mode
				if prev.CDN() && line.Domain == "" {
					line.Domain = prev.Domain
				}
			}
			if prev.CDN() && line.ClientTLS == nil {
				line.ClientTLS = prev.ClientTLS
			}
			if prev.CDN() != line.CDN() {
				return fmt.Errorf("%w: relay line %d: a line cannot be changed into or out of a CDN line: remove it and add another", ErrValidation, i+1)
			}
		}
		if !line.CDN() {
			if line.Domain != "" || line.ClientTLS != nil {
				return fmt.Errorf("%w: relay line %d: only a CDN line has a CDN domain and a client TLS setting", ErrValidation, i+1)
			}
			continue
		}
		if line.EntryPanelID != 0 {
			return fmt.Errorf("%w: relay line %d: a CDN line has no entry server: the CDN is the entry", ErrValidation, i+1)
		}
		d, err := NormalizeCDNDomain(line.Domain)
		if err != nil {
			return fmt.Errorf("%w: relay line %d: %s", ErrValidation, i+1, err)
		}
		line.Domain = d
		address := firstNonEmptyText(line.Address, d)
		var valid bool
		line.Address, valid = normalizeCDNDialAddress(address)
		if !valid {
			return fmt.Errorf("%w: relay line %d: %q is not an IP address or host name (give no port or scheme: the port is its own field)", ErrValidation, i+1, address)
		}
		if line.Port == 0 {
			line.Port = CDNDefaultPort
		}
		if line.Port < 1 || line.Port > 65535 {
			return fmt.Errorf("%w: relay line %d: port %d is out of range (1-65535)", ErrValidation, i+1, line.Port)
		}
		line.SNI, line.Host = strings.TrimSpace(line.SNI), strings.TrimSpace(line.Host)
		if line.SNI == "" {
			line.SNI = d
		}
		if line.Host == "" {
			line.Host = d
		}
		if !validHostname(line.SNI) || !validHostname(line.Host) {
			return fmt.Errorf("%w: relay line %d: the SNI and Host must be plain host names", ErrValidation, i+1)
		}
		if line.ClientTLS == nil {
			on := CDNDefaultClientTLS(line.Port)
			line.ClientTLS = &on
		}
		line.TunnelNodeID = 0
	}
	return nil
}

// Codes of the refusals ValidateCDNRelays words in the reader's language.
const (
	CodeCDNClientTLSTrojan = "cdn_client_tls_trojan"
	CodeCDNClientTLSGRPC   = "cdn_client_tls_grpc"
)

// ValidateCDNRelays refuses CDN lines on a landing that cannot be a CDN origin,
// naming the node and the lines. It is checked on every save of the lines and
// of the landing's own transport, so a CDN line never sits on a node it cannot
// work on.
func ValidateCDNRelays(landing *Node, lines []RelayLine) error {
	var names []string
	for i, line := range lines {
		if line.CDN() {
			names = append(names, firstNonEmptyText(line.Name, line.Domain, strconv.Itoa(i+1)))
		}
	}
	if len(names) == 0 {
		return nil
	}
	if problem := CDNLandingProblem(landing); problem != "" {
		return fmt.Errorf("%w: CDN line %s: %s", ErrValidation, strings.Join(names, ", "), problem)
	}
	trojan := Protocol(strings.ToLower(landing.DesiredProtocol)) == ProtoTrojan
	stream, _ := parseCDNStream(landing)
	grpc := stream.Network == "grpc"
	for i, line := range lines {
		if !line.CDN() || line.ClientTLS == nil || *line.ClientTLS {
			continue
		}
		name := firstNonEmptyText(line.Name, line.Domain, strconv.Itoa(i+1))
		params := map[string]string{"line": name, "node": landing.DisplayName}
		switch {
		case trojan:
			return &CodedValidationError{Code: CodeCDNClientTLSTrojan, Params: params,
				Msg: fmt.Sprintf("CDN line %s of node %q: Trojan always uses TLS, so clients cannot connect to the CDN without it", name, landing.DisplayName)}
		case grpc:
			return &CodedValidationError{Code: CodeCDNClientTLSGRPC, Params: params,
				Msg: fmt.Sprintf("CDN line %s of node %q: gRPC through a CDN needs TLS from the client (Cloudflare serves gRPC over HTTPS only)", name, landing.DisplayName)}
		}
	}
	return nil
}

func firstNonEmptyText(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// CDNWarnings lists what is likely to keep a CDN line from working, or worth a
// second look, given the landing as it is now. None of it blocks saving: a
// CDN other than Cloudflare may allow other ports, and an origin certificate
// issued for the CDN need not show the domain here.
func CDNWarnings(landing *Node, line RelayLine) []CDNIssue {
	if landing == nil || !line.CDN() {
		return nil
	}
	var out []CDNIssue
	add := func(code string, warn bool, kv ...string) {
		p := map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			p[kv[i]] = kv[i+1]
		}
		out = append(out, CDNIssue{Code: code, Warn: warn, Params: p})
	}
	if f := CDNLandingFault(landing); f != nil {
		kv := []string{"reason", CDNLandingProblem(landing), "reason_code", f.Code, "node", landing.DisplayName}
		for k, v := range f.Params {
			kv = append(kv, k, v)
		}
		add(CDNIssueLandingNotCapable, true, kv...)
		return out
	}
	s, _ := parseCDNStream(landing)
	originTLS := s.Security == "tls"
	if originTLS && s.TLSSettings != nil {
		if sn := strings.ToLower(strings.TrimSpace(s.TLSSettings.ServerName)); sn != "" && sn != line.Domain {
			add(CDNIssueServerName, true, "server_name", s.TLSSettings.ServerName, "domain", line.Domain)
		}
		if s.TLSSettings.AllowInsecure {
			add(CDNIssueInsecureIgnored, false)
		}
		switch covered, seen := certCovers(s, line.Domain); {
		case seen && !covered:
			add(CDNIssueCertNotCovering, true, "domain", line.Domain)
		case !seen:
			add(CDNIssueCertUnverifiable, false, "domain", line.Domain)
		}
	}
	if !originTLS {
		// The hop from the CDN to the landing is plain HTTP: a VLESS payload is
		// unencrypted on it, VMess still encrypts its own payload.
		add(CDNIssueOriginPlaintext, true, "protocol", strings.ToUpper(landing.DesiredProtocol))
	}
	clientTLS := line.ClientUsesTLS(originTLS)
	if !clientTLS {
		add(CDNIssueClientPlaintext, true)
	}
	originPorts, kind := CloudflareHTTPSPorts, "HTTPS"
	if !originTLS {
		originPorts, kind = CloudflareHTTPPorts, "HTTP"
	}
	if !slices.Contains(originPorts, landing.DesiredPort) {
		add(CDNIssueOriginPort, true, "port", strconv.Itoa(landing.DesiredPort), "kind", kind, "ports", portList(originPorts))
	}
	linePorts, kind := CloudflareHTTPSPorts, "HTTPS"
	if !clientTLS {
		linePorts, kind = CloudflareHTTPPorts, "HTTP"
	}
	if !slices.Contains(linePorts, line.Port) {
		add(CDNIssueLinePort, true, "port", strconv.Itoa(line.Port), "kind", kind, "ports", portList(linePorts))
	}
	if line.Port != landing.DesiredPort {
		add(CDNIssuePortDiffers, false, "port", strconv.Itoa(line.Port), "origin_port", strconv.Itoa(landing.DesiredPort))
	}
	if !strings.EqualFold(line.SNI, line.Domain) && clientTLS {
		add(CDNIssueSNIDiffers, false, "sni", line.SNI, "domain", line.Domain)
	}
	if !strings.EqualFold(line.Host, line.Domain) {
		add(CDNIssueHostDiffers, false, "host", line.Host, "domain", line.Domain)
	}
	if s.Network == "grpc" {
		if !clientTLS || line.Port != 443 || landing.DesiredPort != 443 {
			add(CDNIssueCloudflareGRPC, true)
		}
		if clientTLS && !strings.EqualFold(line.Host, line.SNI) {
			add(CDNIssueGRPCAuthority, true, "host", line.Host, "sni", line.SNI)
		}
	}
	if s.Network == "httpupgrade" && s.HTTPUpgradeSettings != nil && headerHostFold(s.HTTPUpgradeSettings.Headers) != "" {
		// Xray does not start an HTTPUpgrade inbound with a Host inside headers;
		// the host field is the only place for it.
		add(CDNIssueHTTPUpgradeHeader, true)
	}
	if oh := s.originHost(); oh != "" && !strings.EqualFold(oh, line.Host) {
		add(CDNIssueOriginHost, true, "origin_host", oh, "host", line.Host)
	}
	return out
}

// certCovers reports whether an inline certificate of the landing covers the
// domain. seen is false when no certificate could be read (a file path, or none
// configured), in which case nothing can be said.
func certCovers(s cdnStream, domain string) (covered, seen bool) {
	if s.TLSSettings == nil {
		return false, false
	}
	for _, c := range s.TLSSettings.Certificates {
		if len(c.Certificate) == 0 {
			continue
		}
		data := []byte(strings.Join(c.Certificate, "\n"))
		for {
			var block *pem.Block
			block, data = pem.Decode(data)
			if block == nil {
				break
			}
			if block.Type != "CERTIFICATE" {
				continue
			}
			leaf, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				continue
			}
			seen = true
			if leaf.VerifyHostname(domain) == nil {
				return true, true
			}
			// Only the leaf names the host; the rest of the chain is issuers.
			break
		}
	}
	return false, seen
}

// Label is how a line is named where it has no name of its own: a CDN line by
// its domain (its address is the optimised IP), any other by its address.
func (r RelayLine) Label() string {
	if strings.TrimSpace(r.Name) != "" {
		return r.Name
	}
	if r.CDN() && r.Domain != "" {
		return r.Domain
	}
	return r.Address
}
