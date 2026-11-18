package domain

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"
)

// An SNI front (transit.sni.v1) shares one TCP port of a Passwall Node server
// among several TLS or REALITY uses, routing each raw connection on the SNI of
// its ClientHello. This file holds the panel's model of one: what the
// administrator wrote, with every port and name explicit.
//
// SETTINGS APPLY LITERALLY. Nothing here is derived at send time: a front's
// port, each listener's loopback port, each route's names and the default site
// are the values the administrator saved. Suggestions (a random loopback port,
// a camouflage site, the names a listener already serves) are only ever
// pre-fills in the editor.

// FrontTargetKind says what a front hands a connection to.
type FrontTargetKind string

const (
	// FrontTargetListener is a listener (node) of the same server, moved to a
	// loopback port of the front's own.
	FrontTargetListener FrontTargetKind = "listener"
	// FrontTargetForward is a managed relay line of this server relayed raw to
	// its landing by the landing's SNI.
	FrontTargetForward FrontTargetKind = "forward"
	// FrontTargetLink is a tunnel exit (a vless_reality link on this server).
	// RESERVED: the wire contract has it and the model leaves the seam, but
	// PSP does not yet let a link share a port, so a save naming it is
	// refused. Adding it means a per-link port and REALITY server names that
	// follow the route, in service/transit/link.go.
	FrontTargetLink FrontTargetKind = "link"
	// FrontTargetExternal is a real site elsewhere. Only a front's Default may
	// be one.
	FrontTargetExternal FrontTargetKind = "external"
)

// FrontTarget is where one route (or the default) sends a connection.
type FrontTarget struct {
	Kind FrontTargetKind `json:"kind"`
	// NodeID is the listener's node, or the landing node of a forward.
	NodeID int64 `json:"node_id,omitempty"`
	// LineID is the relay line of a forward target.
	LineID string `json:"line_id,omitempty"`
	// Host and Port are the external site: a canonical IP or a lowercase name.
	Host string `json:"host,omitempty"`
	Port int    `json:"port,omitempty"`
}

// FrontListener moves one node's listener onto a loopback port behind the
// front. The node keeps its public port in its own config.
type FrontListener struct {
	NodeID       int64 `json:"node_id"`
	LoopbackPort int   `json:"loopback_port"`
}

// FrontRoute sends the connections whose SNI is one of ServerNames to Target.
// Names are exact, lowercase and sorted; a name appears once on a front.
type FrontRoute struct {
	ServerNames []string    `json:"server_names"`
	Target      FrontTarget `json:"target"`
}

// TransitFront is one shared port of a server.
type TransitFront struct {
	ID        int64
	PanelID   int64
	Port      int
	Listeners []FrontListener
	Routes    []FrontRoute
	// Default takes every connection no route names, SNI absent included. It is
	// an external site.
	Default   FrontTarget
	CreatedAt time.Time
	UpdatedAt time.Time
}

// FrontSpec is the stored body of a front: everything except the row's
// identity and the (server, port) it is unique on.
type FrontSpec struct {
	Listeners []FrontListener `json:"listeners,omitempty"`
	Routes    []FrontRoute    `json:"routes"`
	Default   FrontTarget     `json:"default"`
}

// Spec returns the stored body.
func (f TransitFront) Spec() FrontSpec {
	return FrontSpec{Listeners: f.Listeners, Routes: f.Routes, Default: f.Default}
}

// EncodeSpec is the stored form of a front's body.
func (f TransitFront) EncodeSpec() (string, error) {
	raw, err := json.Marshal(f.Spec())
	if err != nil {
		return "", fmt.Errorf("encode front: %w", err)
	}
	return string(raw), nil
}

// DecodeFrontSpec reads a stored body back into the front.
func (f *TransitFront) DecodeFrontSpec(raw string) error {
	var spec FrontSpec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		return fmt.Errorf("decode front: %w", err)
	}
	f.Listeners, f.Routes, f.Default = spec.Listeners, spec.Routes, spec.Default
	return nil
}

// HasListener reports whether the node's listener is moved behind this front.
func (f TransitFront) HasListener(nodeID int64) bool {
	for _, item := range f.Listeners {
		if item.NodeID == nodeID {
			return true
		}
	}
	return false
}

// Targets lists every target of the front, the default last.
func (f TransitFront) Targets() []FrontTarget {
	out := make([]FrontTarget, 0, len(f.Routes)+1)
	for _, route := range f.Routes {
		out = append(out, route.Target)
	}
	return append(out, f.Default)
}

// Loopback ports. A fronted listener binds 127.0.0.1:LoopbackPort, a socket
// like any other on the host, so the administrator sees and edits it. The
// pre-fill range sits just under the range the Node reserves for its own probe
// sockets.
const (
	FrontLoopbackSuggestLow  = 30000
	FrontLoopbackSuggestHigh = 30999
	// NodeProbeLoopbackLow and High are the loopback ports Node uses internally
	// for link probes. A loopback port or any other port the panel hands out
	// must stay out of it; the port-usage view lists it so nobody has to know.
	NodeProbeLoopbackLow  = nodeprotocol.ProbePortFirst
	NodeProbeLoopbackHigh = nodeprotocol.ProbePortLast
	// DefaultFrontPort is the pre-fill of a new front's port. Any TCP port may
	// be shared.
	DefaultFrontPort = 443
)

// InNodeProbeRange reports whether a port is in Node's internal probe range.
func InNodeProbeRange(port int) bool {
	return port >= NodeProbeLoopbackLow && port <= NodeProbeLoopbackHigh
}

// Ports the Node reserves on loopback for itself. A loopback port of a
// fronted listener must not be one, and a shared port must not either (a front
// binds every address, loopback included).
var NodeReservedPorts = []int{10085}

// NodeReservedPort reports whether a port is one the Node keeps for itself,
// and what it is.
func NodeReservedPort(port int) (string, bool) {
	for _, reserved := range NodeReservedPorts {
		if port == reserved {
			return "core API", true
		}
	}
	return "", false
}

// FrontableListener is whether a node's listener can sit behind a front, with
// the server names it already serves (for pre-filling a route) and, when it
// cannot, the reason. The rule is Node's own (internal/core/xray/front.go,
// frontUnfit): the transport is TCP ("", tcp or raw), the security is TLS or
// REALITY, and the listener does not itself accept a PROXY protocol header,
// because the front writes one. Shadowsocks (SS-2022 included), the UDP
// protocols, WebSocket/gRPC/HTTP/XHTTP transports and plain listeners keep
// their own ports.
type FrontableListener struct {
	OK bool
	// Reason is a stable code when OK is false.
	Reason string
	// Security is "tls" or "reality" when OK.
	Security string
	// ServerNames are the names the listener serves, sorted and lowercase.
	ServerNames []string
	// RenderedName is the SNI a subscription gives this listener's clients.
	RenderedName string
}

// Reasons a listener cannot be fronted.
const (
	FrontRefusalNoSNI         = "no_sni"
	FrontRefusalUDP           = "udp"
	FrontRefusalSeparator     = "separator"
	FrontRefusalNoTLS         = "no_tls"
	FrontRefusalBadSettings   = "bad_settings"
	FrontRefusalTransport     = "transport"
	FrontRefusalProxyProtocol = "proxy_protocol"
	FrontRefusalNoName        = "no_name"
)

// RenderedName is the SNI a subscription gives the client of this listener:
// the first of its REALITY server names, or its TLS server name (render reads
// them the same way). Routes must include it, or the client's connections go
// to the default site.

// streamOf reads the stream settings the way Node (and Xray) reads them: a
// generic document with EXACT keys. Go's struct decoding matches keys without
// regard to case, so a duplicate key spelled differently could make the panel
// accept what Node refuses; a map has no such looseness.
func streamOf(raw string) (map[string]any, bool) {
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil || doc == nil {
		return nil, false
	}
	return doc, true
}

func objectOf(doc map[string]any, key string) map[string]any {
	out, _ := doc[key].(map[string]any)
	return out
}

func stringOf(doc map[string]any, key string) string {
	out, _ := doc[key].(string)
	return out
}

func acceptsProxy(doc map[string]any, key string) bool {
	accepts, _ := objectOf(doc, key)["acceptProxyProtocol"].(bool)
	return accepts
}

// tlsOverTCP reads a node as a TCP TLS or REALITY listener with a server name:
// the part of the rule that holds for a relay line's landing too (which is not
// moved, so a PROXY header setting does not matter to it). It is stricter than
// Node in one way: only VLESS, VMess and Trojan, the protocols whose clients
// send the server name the subscription renders.
func tlsOverTCP(n *Node) (stream map[string]any, f FrontableListener) {
	if n == nil || n.IsSeparator() {
		return nil, FrontableListener{Reason: FrontRefusalSeparator}
	}
	switch Protocol(strings.ToLower(n.DesiredProtocol)) {
	case ProtoVLESS, ProtoVMess, ProtoTrojan:
	case ProtoHysteria2, ProtoTUIC:
		return nil, FrontableListener{Reason: FrontRefusalUDP}
	default:
		// Shadowsocks and anything else without a server name to route on.
		return nil, FrontableListener{Reason: FrontRefusalNoSNI}
	}
	if strings.TrimSpace(n.StreamSettings) == "" {
		return nil, FrontableListener{Reason: FrontRefusalNoTLS}
	}
	stream, ok := streamOf(n.StreamSettings)
	if !ok {
		return nil, FrontableListener{Reason: FrontRefusalBadSettings}
	}
	switch strings.ToLower(strings.TrimSpace(stringOf(stream, "network"))) {
	case "", "tcp", "raw":
	case "kcp", "mkcp", "quic":
		return stream, FrontableListener{Reason: FrontRefusalUDP}
	default:
		return stream, FrontableListener{Reason: FrontRefusalTransport}
	}
	var names []string
	security := strings.ToLower(strings.TrimSpace(stringOf(stream, "security")))
	switch security {
	case "reality":
		if list, ok := objectOf(stream, "realitySettings")["serverNames"].([]any); ok {
			for _, item := range list {
				text, _ := item.(string)
				names = append(names, text)
			}
		}
	case "tls":
		names = []string{stringOf(objectOf(stream, "tlsSettings"), "serverName")}
	default:
		return stream, FrontableListener{Reason: FrontRefusalNoTLS}
	}
	// What the subscription renders is the first name as written.
	rendered := ""
	if len(names) > 0 {
		rendered = NormalizeFrontName(names[0])
	}
	if rendered == "" || strings.Contains(rendered, "*") {
		return stream, FrontableListener{Reason: FrontRefusalNoName}
	}
	cleaned := make([]string, 0, len(names))
	seen := map[string]bool{}
	for _, name := range names {
		name = NormalizeFrontName(name)
		if name == "" || strings.Contains(name, "*") || seen[name] {
			continue
		}
		seen[name] = true
		cleaned = append(cleaned, name)
	}
	sort.Strings(cleaned)
	return stream, FrontableListener{OK: true, Security: security, ServerNames: cleaned, RenderedName: rendered}
}

// FrontableOf decides whether the node's listener can be fronted.
func FrontableOf(n *Node) FrontableListener {
	stream, f := tlsOverTCP(n)
	if !f.OK {
		return f
	}
	if acceptsProxy(stream, "sockopt") || acceptsProxy(stream, "rawSettings") || acceptsProxy(stream, "tcpSettings") {
		return FrontableListener{Reason: FrontRefusalProxyProtocol}
	}
	return f
}

// LandingOfRelay decides whether a node can be the landing of a relay line
// that a front routes by SNI: a TCP TLS or REALITY listener, whose names are
// the SNIs its clients send. Anything else (a UDP protocol, a plain or
// WebSocket listener) has no SNI to route by.
func LandingOfRelay(n *Node) FrontableListener {
	_, f := tlsOverTCP(n)
	return f
}

// UnacceptedFrontName returns the first name that is not one the listener
// serves, or "". A listener that serves any name (a TLS listener with no
// configured name) accepts all of them.
func UnacceptedFrontName(served []string, names []string) string {
	if len(served) == 0 {
		return ""
	}
	ok := make(map[string]bool, len(served))
	for _, name := range served {
		ok[name] = true
	}
	for _, name := range names {
		if !ok[NormalizeFrontName(name)] {
			return name
		}
	}
	return ""
}

// MissingRenderedName reports whether routes (the names of every route that
// reaches a target, together) leave out the SNI the target's clients send.
func MissingRenderedName(rendered string, names []string) bool {
	for _, name := range names {
		if NormalizeFrontName(name) == rendered {
			return false
		}
	}
	return true
}

// NormalizeFrontName lowercases a server name and drops a trailing dot, the
// form routes are written and compared in.
func NormalizeFrontName(name string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
}
