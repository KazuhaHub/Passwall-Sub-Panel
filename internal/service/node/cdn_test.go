package node

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

const (
	cdnWSStream      = `{"network":"ws","security":"tls","tlsSettings":{"serverName":"cdn.example.com"},"wsSettings":{"path":"/ws"}}`
	cdnRealityStream = `{"network":"tcp","security":"reality","realitySettings":{}}`
)

func cdnLanding(stream string) domain.Node {
	return domain.Node{ID: 1, PanelID: 1, InboundID: 3, DisplayName: "Tokyo", Region: "JP", DesiredProtocol: "vless", DesiredPort: 443, StreamSettings: stream}
}

// A CDN line saved on a node that can carry it is stored with what it left out
// filled from its domain, at the default factor; the lines already on the node
// are untouched.
func TestUpdateMetadataStoresCDNLineFilled(t *testing.T) {
	stored := cdnLanding(cdnWSStream)
	stored.Relays = []domain.RelayLine{{ID: "f", Address: "gz.relay.cn", Port: 20001, Region: "JP", Enabled: true}}
	repo := &labelNodeRepo{stored: stored}
	svc := &Service{nodes: repo}
	next := cdnLanding(cdnWSStream)
	next.Relays = []domain.RelayLine{
		{ID: "f", Address: "gz.relay.cn", Port: 20001, Enabled: true, ModeUnresolved: true},
		{Mode: domain.RelayModeCDN, Domain: " CDN.Example.com ", Enabled: true},
	}
	if err := svc.UpdateMetadata(context.Background(), &next); err != nil {
		t.Fatal(err)
	}
	got := repo.written.Relays
	if len(got) != 2 || got[0].Address != "gz.relay.cn" || got[0].Mode != "" || got[0].Domain != "" {
		t.Fatalf("the forward line changed: %+v", got)
	}
	c := got[1]
	if !c.CDN() || c.Domain != "cdn.example.com" || c.Address != "cdn.example.com" || c.Port != 443 || c.SNI != "cdn.example.com" || c.Host != "cdn.example.com" || c.ID == "" {
		t.Fatalf("cdn line = %+v", c)
	}
	if c.EffectiveTrafficFactor() != 1000 || c.EntryPanelID != 0 || !c.Publishable() {
		t.Fatalf("a CDN line is one-for-one, has no entry and is published with its landing: %+v", c)
	}
}

// Refusals block the save, name the node, and write nothing.
func TestUpdateMetadataRefusesCDNLineOnIncapableLanding(t *testing.T) {
	for name, tc := range map[string]struct {
		stream string
		proto  string
		want   string
	}{
		"reality":   {cdnRealityStream, "vless", "REALITY"},
		"raw tcp":   {`{"network":"tcp","security":"tls"}`, "trojan", "raw TCP"},
		"hysteria2": {`{"network":"tcp","security":"tls"}`, "hysteria2", "Hysteria2"},
		"xhttp":     {`{"network":"xhttp","security":"tls"}`, "vless", "XHTTP"},
	} {
		stored := cdnLanding(tc.stream)
		stored.DesiredProtocol = tc.proto
		repo := &labelNodeRepo{stored: stored}
		svc := &Service{nodes: repo}
		next := stored
		next.Relays = []domain.RelayLine{{Mode: domain.RelayModeCDN, Domain: "cdn.example.com", Name: "CF", Enabled: true}}
		err := svc.UpdateMetadata(context.Background(), &next)
		if !errors.Is(err, domain.ErrValidation) || !strings.Contains(err.Error(), `"Tokyo"`) || !strings.Contains(err.Error(), tc.want) || repo.written != nil {
			t.Errorf("%s: err = %v written = %v", name, err, repo.written)
		}
	}
}

// A line is never turned into a CDN line or back: it is removed and added.
func TestUpdateMetadataRefusesChangingCDNKind(t *testing.T) {
	stored := cdnLanding(cdnWSStream)
	stored.Relays = []domain.RelayLine{
		{ID: "c", Mode: domain.RelayModeCDN, Domain: "cdn.example.com", Address: "cdn.example.com", Port: 443, SNI: "cdn.example.com", Host: "cdn.example.com", Region: "JP", Enabled: true},
		{ID: "f", Address: "gz.relay.cn", Port: 20001, Region: "JP", Enabled: true},
	}
	repo := &labelNodeRepo{stored: stored}
	svc := &Service{nodes: repo}
	toForward := cdnLanding(cdnWSStream)
	toForward.Relays = []domain.RelayLine{{ID: "c", Mode: "", Address: "x", Port: 1, Enabled: true}, {ID: "f", Address: "gz.relay.cn", Port: 20001, Enabled: true}}
	toCDN := cdnLanding(cdnWSStream)
	toCDN.Relays = []domain.RelayLine{stored.Relays[0], {ID: "f", Mode: domain.RelayModeCDN, Domain: "cdn.example.com", Enabled: true}}
	for name, n := range map[string]domain.Node{"cdn to forward": toForward, "forward to cdn": toCDN} {
		n := n
		if err := svc.UpdateMetadata(context.Background(), &n); !errors.Is(err, domain.ErrValidation) || repo.written != nil {
			t.Errorf("%s: err %v written %v", name, err, repo.written)
		}
	}
	// Editing a CDN line in place (address, port, SNI) is fine and keeps its id.
	edit := cdnLanding(cdnWSStream)
	edit.Relays = []domain.RelayLine{stored.Relays[0], stored.Relays[1]}
	edit.Relays[0].Address, edit.Relays[0].Port = "104.16.1.1", 8443
	edit.Relays[0].ModeUnresolved, edit.Relays[0].Domain = true, ""
	if err := svc.UpdateMetadata(context.Background(), &edit); err != nil {
		t.Fatal(err)
	}
	if c := repo.written.Relays[0]; c.ID != "c" || c.Address != "104.16.1.1" || c.Port != 8443 || c.Domain != "cdn.example.com" || !c.CDN() {
		t.Fatalf("edited line = %+v", c)
	}
}

// The landing's own transport cannot be changed under its CDN lines.
func TestUpdateInboundConfigRefusedUnderCDNLines(t *testing.T) {
	n := cdnLanding(cdnWSStream)
	n.Relays = []domain.RelayLine{{ID: "c", Name: "CF", Mode: domain.RelayModeCDN, Domain: "cdn.example.com", Address: "a", Port: 443, Enabled: true}}
	repo := &captureNodeRepo{node: &n}
	client := &stubXUIClient{}
	svc := &Service{nodes: repo, pool: stubXUIPool{c: client}}

	reality := ports.InboundSpec{Protocol: "vless", Port: 443, Settings: `{"clients":[]}`, StreamSettings: cdnRealityStream}
	err := svc.UpdateInboundConfig(context.Background(), 1, reality)
	if !errors.Is(err, domain.ErrValidation) || !strings.Contains(err.Error(), `"Tokyo"`) || !strings.Contains(err.Error(), "REALITY") || repo.updateCfg != nil || client.updated != nil {
		t.Fatalf("err = %v, written %v pushed %v", err, repo.updateCfg, client.updated)
	}
	// Moving to gRPC keeps it valid.
	grpc := ports.InboundSpec{Protocol: "vless", Port: 443, Settings: `{"clients":[]}`, StreamSettings: `{"network":"grpc","security":"tls"}`}
	if err := svc.UpdateInboundConfig(context.Background(), 1, grpc); err != nil {
		t.Fatalf("a transport a CDN can carry: %v", err)
	}
	// A node with no CDN line is never judged.
	plain := cdnLanding(cdnRealityStream)
	svc = &Service{nodes: &captureNodeRepo{node: &plain}, pool: stubXUIPool{c: &stubXUIClient{}}}
	if err := svc.UpdateInboundConfig(context.Background(), 1, reality); err != nil {
		t.Fatalf("plain node: %v", err)
	}
}

// Creating a node judges its CDN lines against the transport it is created with.
func TestCreateInboundJudgesCDNLinesAgainstSpec(t *testing.T) {
	spec := func(stream string) ports.InboundSpec {
		return ports.InboundSpec{Protocol: "vless", Port: 443, Settings: `{"clients":[]}`, StreamSettings: stream}
	}
	mk := func() (*Service, *captureNodeRepo, *stubXUIClient) {
		repo := &captureNodeRepo{}
		client := &stubXUIClient{addID: 5}
		return &Service{nodes: repo, pool: stubXUIPool{c: client}, groups: emptyGroups{}}, repo, client
	}
	line := func() []domain.RelayLine {
		return []domain.RelayLine{{Mode: domain.RelayModeCDN, Domain: "cdn.example.com", Enabled: true}}
	}
	svc, repo, client := mk()
	n := &domain.Node{DisplayName: "Tokyo", Region: "JP", PanelID: 1, Relays: line()}
	if err := svc.CreateInbound(context.Background(), n, spec(cdnRealityStream)); !errors.Is(err, domain.ErrValidation) || client.added != nil || repo.created != nil {
		t.Fatalf("reality: %v added %v created %v", err, client.added, repo.created)
	}
	svc, repo, client = mk()
	n = &domain.Node{DisplayName: "Tokyo", Region: "JP", PanelID: 1, Relays: line()}
	if err := svc.CreateInbound(context.Background(), n, spec(cdnWSStream)); err != nil {
		t.Fatal(err)
	}
	if repo.created == nil || len(repo.created.Relays) != 1 || repo.created.Relays[0].Address != "cdn.example.com" || repo.created.Relays[0].Port != 443 {
		t.Fatalf("created = %+v", repo.created)
	}
}

// The CDN lines are judged against the node as the save leaves it: a vision
// flow set in the same edit is refused, clearing it is accepted.
func TestUpdateMetadataJudgesCDNLinesWithTheFlowBeingSaved(t *testing.T) {
	stored := cdnLanding(cdnWSStream)
	stored.Flow = "xtls-rprx-vision"
	stored.Relays = []domain.RelayLine{{ID: "c", Name: "CF", Mode: domain.RelayModeCDN, Domain: "cdn.example.com", Address: "cdn.example.com", Port: 443,
		SNI: "cdn.example.com", Host: "cdn.example.com", Region: "JP", Enabled: true}}
	repo := &labelNodeRepo{stored: stored}
	svc := &Service{nodes: repo}

	withFlow := cdnLanding(cdnWSStream)
	withFlow.Flow = "xtls-rprx-vision"
	withFlow.Relays = []domain.RelayLine{stored.Relays[0]}
	withFlow.Relays[0].ModeUnresolved = true
	if err := svc.UpdateMetadata(context.Background(), &withFlow); !errors.Is(err, domain.ErrValidation) || !strings.Contains(err.Error(), "xtls-rprx-vision") || repo.written != nil {
		t.Fatalf("flow set with a CDN line: %v written %v", err, repo.written)
	}
	// Stored without a flow, then the same edit sets one: refused too.
	stored.Flow = ""
	repo.stored = stored
	if err := svc.UpdateMetadata(context.Background(), &withFlow); !errors.Is(err, domain.ErrValidation) || repo.written != nil {
		t.Fatalf("flow set in the edit: %v", err)
	}
	// Stored with the flow, the edit clears it: accepted.
	stored.Flow = "xtls-rprx-vision"
	repo.stored = stored
	cleared := cdnLanding(cdnWSStream)
	cleared.Relays = []domain.RelayLine{stored.Relays[0]}
	cleared.Relays[0].ModeUnresolved = true
	if err := svc.UpdateMetadata(context.Background(), &cleared); err != nil || repo.written == nil || repo.written.Flow != "" {
		t.Fatalf("flow cleared: %v", err)
	}
}

// Import judges the lines after the inbound's own flow has been copied.
func TestImportExistingJudgesCDNLinesWithTheAdoptedFlow(t *testing.T) {
	inb := &ports.Inbound{ID: 3, Port: 443, Protocol: "vless", StreamSettings: cdnWSStream,
		Settings: `{"clients":[{"id":"x","email":"e","flow":"xtls-rprx-vision"}],"decryption":"none"}`}
	repo := &captureNodeRepo{}
	svc := &Service{nodes: repo, pool: stubXUIPool{c: &stubXUIClient{getResp: inb}}, groups: emptyGroups{}}
	n := &domain.Node{DisplayName: "Tokyo", Region: "JP", PanelID: 1, InboundID: 3,
		Relays: []domain.RelayLine{{Mode: domain.RelayModeCDN, Domain: "cdn.example.com", Enabled: true}}}
	err := svc.ImportExisting(context.Background(), n)
	if !errors.Is(err, domain.ErrValidation) || !strings.Contains(err.Error(), "xtls-rprx-vision") || repo.created != nil {
		t.Fatalf("err %v created %v", err, repo.created)
	}
}

// A plain-HTTP origin (Cloudflare Flexible) is accepted for VLESS/VMess over
// WS, and the line's own client TLS setting is filled from its port.
func TestUpdateMetadataAcceptsAPlainOriginAndDefaultsClientTLS(t *testing.T) {
	plain := `{"network":"ws","security":"none","wsSettings":{"path":"/ws"}}`
	stored := cdnLanding(plain)
	stored.DesiredPort = 8080
	repo := &labelNodeRepo{stored: stored}
	svc := &Service{nodes: repo}
	next := stored
	next.Relays = []domain.RelayLine{
		{Mode: domain.RelayModeCDN, Domain: "a.example.com", Port: 8080, Enabled: true},
		{Mode: domain.RelayModeCDN, Domain: "b.example.com", Enabled: true},
	}
	if err := svc.UpdateMetadata(context.Background(), &next); err != nil {
		t.Fatal(err)
	}
	a, b := repo.written.Relays[0], repo.written.Relays[1]
	if a.ClientTLS == nil || *a.ClientTLS || b.ClientTLS == nil || !*b.ClientTLS {
		t.Fatalf("client TLS defaults: %v %v (HTTP port off, HTTPS port on)", a.ClientTLS, b.ClientTLS)
	}
	// Trojan, even over TLS, cannot be reached without TLS from the client.
	trojan := cdnLanding(cdnWSStream)
	trojan.DesiredProtocol = "trojan"
	off := false
	trojan.Relays = []domain.RelayLine{{Mode: domain.RelayModeCDN, Domain: "a.example.com", Port: 8080, ClientTLS: &off, Enabled: true}}
	repo2 := &labelNodeRepo{stored: trojan}
	if err := (&Service{nodes: repo2}).UpdateMetadata(context.Background(), &trojan); !errors.Is(err, domain.ErrValidation) || !strings.Contains(err.Error(), "Trojan") || repo2.written != nil {
		t.Fatalf("trojan without client TLS: %v", err)
	}
}
