package node

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func guardService(repo *factorNodes, canGuard bool) *Service {
	svc := &Service{nodes: repo}
	svc.SetSourceGuardCheck(func(context.Context, int64) bool { return canGuard })
	return svc
}

func codeOf(t *testing.T, err error) string {
	t.Helper()
	var coded *domain.CodedValidationError
	if !errors.As(err, &coded) || !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("err = %v, want a coded validation error", err)
	}
	return coded.Code
}

func TestSourceGuardRefusesDifferingFactorsOnALandingThatCannotGuard(t *testing.T) {
	repo := &factorNodes{nodes: map[int64]*domain.Node{
		1: {ID: 1, PanelID: 10, DisplayName: "hk", Relays: []domain.RelayLine{{ID: "r", Name: "r", Address: "r.example", Enabled: true}}},
	}}
	svc := guardService(repo, false)
	ctx := context.Background()

	// A differing line factor, by the bulk editor and by the full editor.
	_, err := svc.SetLineFactors(ctx, 10, []LineFactorChange{{Line: domain.LineKey{NodeID: 1, LineID: "r"}, FactorMilli: 2000}})
	if codeOf(t, err) != CodeSourceGuardFactorMismatch || len(repo.written) != 0 {
		t.Fatalf("bulk: %v (written %d)", err, len(repo.written))
	}
	_, err = svc.SetLineFactors(ctx, 10, []LineFactorChange{{Line: domain.DirectLine(1), FactorMilli: 2000}})
	if codeOf(t, err) != CodeSourceGuardFactorMismatch {
		t.Fatalf("a direct factor that now differs from the line: %v", err)
	}
	// Moving both together is fine.
	if _, err := svc.SetLineFactors(ctx, 10, []LineFactorChange{
		{Line: domain.DirectLine(1), FactorMilli: 2000}, {Line: domain.LineKey{NodeID: 1, LineID: "r"}, FactorMilli: 2000},
	}); err != nil {
		t.Fatalf("equal factors: %v", err)
	}

	// A tunnel line has a listener of its own and is exempt.
	repo.nodes[1].Relays = []domain.RelayLine{{ID: "t", Mode: domain.RelayModeTunnel, EntryPanelID: 3, TrafficFactorMilli: milli(3000)}}
	repo.nodes[1].TrafficFactorMilli = nil
	if err := checkOnly(svc, repo.nodes[1]); err != nil {
		t.Fatalf("tunnel line: %v", err)
	}
}

func checkOnly(svc *Service, n *domain.Node) error {
	prev := *n
	prev.Relays = nil
	return svc.checkSourceGuard(context.Background(), &prev, n)
}

func TestSourceGuardNeedsAddressesForADifferingExternalLineOnALandingThatCanGuard(t *testing.T) {
	svc := guardService(&factorNodes{}, true)
	two := milli(2000)
	landing := func(lines ...domain.RelayLine) *domain.Node {
		return &domain.Node{ID: 1, PanelID: 10, DisplayName: "hk", Relays: lines}
	}
	prev := landing()
	external := domain.RelayLine{ID: "e", Name: "e", Address: "e.example", TrafficFactorMilli: two}
	if codeOf(t, svc.checkSourceGuard(context.Background(), prev, landing(external))) != CodeSourceGuardNeedsCIDRs {
		t.Fatal("an external line at another price without addresses was accepted")
	}
	external.SourceCIDRs = []string{"192.0.2.0/24"}
	for name, line := range map[string]domain.RelayLine{
		"with addresses":  external,
		"managed forward": {ID: "m", Address: "m.example", EntryPanelID: 7, TrafficFactorMilli: two},
		"cdn":             {ID: "c", Mode: domain.RelayModeCDN, TrafficFactorMilli: two},
		"same factor":     {ID: "s", Address: "s.example"},
		"tunnel":          {ID: "t", Mode: domain.RelayModeTunnel, EntryPanelID: 7, TrafficFactorMilli: two},
	} {
		if err := svc.checkSourceGuard(context.Background(), prev, landing(line)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// What was already wrong before a save does not stop an unrelated edit; only a
// line the save introduces or changes is refused.
func TestSourceGuardDoesNotBlockUnrelatedEditsOfAnAlreadyMismatchedLanding(t *testing.T) {
	svc := guardService(&factorNodes{}, false)
	mismatched := func(name string, factor *int) *domain.Node {
		return &domain.Node{ID: 1, PanelID: 10, DisplayName: name, Relays: []domain.RelayLine{{ID: "r", Name: "r", Address: "r", TrafficFactorMilli: factor}}}
	}
	before := mismatched("hk", milli(2000))
	if err := svc.checkSourceGuard(context.Background(), before, mismatched("renamed", milli(2000))); err != nil {
		t.Fatalf("a rename of a node that was already mismatched: %v", err)
	}
	if err := svc.checkSourceGuard(context.Background(), before, mismatched("hk", milli(3000))); codeOf(t, err) != CodeSourceGuardFactorMismatch {
		t.Fatalf("a changed factor: %v", err)
	}
}

func TestResolveSourceCIDRs(t *testing.T) {
	previous := &domain.Node{Relays: []domain.RelayLine{{ID: "a", SourceCIDRs: []string{"192.0.2.0/24"}}}}
	next := &domain.Node{Relays: []domain.RelayLine{
		{ID: "a", SourceCIDRsUnresolved: true},
		{ID: "b", SourceCIDRs: []string{"203.0.113.9", " 10.0.0.0/8 "}},
		{ID: "c", Mode: domain.RelayModeTunnel, SourceCIDRs: []string{"192.0.2.1"}},
	}}
	if err := resolveSourceCIDRs(previous, next); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(next.Relays[0].SourceCIDRs, []string{"192.0.2.0/24"}) ||
		!reflect.DeepEqual(next.Relays[1].SourceCIDRs, []string{"10.0.0.0/8", "203.0.113.9/32"}) ||
		next.Relays[2].SourceCIDRs != nil {
		t.Fatalf("relays = %+v", next.Relays)
	}
	bad := &domain.Node{Relays: []domain.RelayLine{{ID: "x", SourceCIDRs: []string{"10.0.0.1/8"}}}}
	if err := resolveSourceCIDRs(nil, bad); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("host bits: %v", err)
	}
	// An explicit empty list clears.
	clear := &domain.Node{Relays: []domain.RelayLine{{ID: "a", SourceCIDRs: []string{}}}}
	if err := resolveSourceCIDRs(previous, clear); err != nil || len(clear.Relays[0].SourceCIDRs) != 0 {
		t.Fatalf("clear: %v %+v", err, clear.Relays)
	}
}

func cdnLine(id string, factor int) domain.RelayLine {
	return domain.RelayLine{ID: id, Name: id, Mode: domain.RelayModeCDN, Domain: id + ".example.com", Enabled: true, TrafficFactorMilli: milli(factor)}
}

// Lines that arrive from the same addresses cannot be priced apart: two CDN
// lines on the Cloudflare default at different factors are refused, at one
// factor they are allowed.
func TestSourceGuardRefusesCDNLinesAtDifferentFactorsOnTheCloudflareDefault(t *testing.T) {
	svc := guardService(&factorNodes{}, true)
	landing := func(lines ...domain.RelayLine) *domain.Node {
		return &domain.Node{ID: 1, PanelID: 10, DisplayName: "hk", Relays: lines}
	}
	prev := landing()
	err := svc.checkSourceGuard(context.Background(), prev, landing(cdnLine("a", 2000), cdnLine("b", 3000)))
	if codeOf(t, err) != CodeSourceGuardOverlap || !strings.Contains(err.Error(), "same addresses must have the same factor") {
		t.Fatalf("different factors: %v", err)
	}
	if err := svc.checkSourceGuard(context.Background(), prev, landing(cdnLine("a", 2000), cdnLine("b", 2000))); err != nil {
		t.Fatalf("the same factor: %v", err)
	}
	// A CDN class and an external class with disjoint addresses are fine.
	ext := domain.RelayLine{ID: "e", Name: "e", Address: "e", Enabled: true, TrafficFactorMilli: milli(3000), SourceCIDRs: []string{"192.0.2.0/24"}}
	if err := svc.checkSourceGuard(context.Background(), prev, landing(cdnLine("a", 2000), ext)); err != nil {
		t.Fatalf("disjoint: %v", err)
	}
	// The already-overlapping node does not block an unrelated edit.
	bad := landing(cdnLine("a", 2000), cdnLine("b", 3000))
	if err := svc.checkSourceGuard(context.Background(), bad, bad); err != nil {
		t.Fatalf("unrelated edit: %v", err)
	}
}

func TestSourceGuardRefusesMoreThan64PricedClassesAndOversizedClasses(t *testing.T) {
	svc := guardService(&factorNodes{}, true)
	prev := &domain.Node{ID: 1, PanelID: 10, DisplayName: "hk"}
	next := &domain.Node{ID: 1, PanelID: 10, DisplayName: "hk"}
	for i := 0; i < domain.MaxPricedClasses+1; i++ {
		next.Relays = append(next.Relays, domain.RelayLine{
			ID: fmt.Sprintf("l%d", i), Name: "l", Address: "x", Enabled: true,
			SourceCIDRs: []string{fmt.Sprintf("10.%d.0.0/16", i)}, TrafficFactorMilli: milli(2000 + i),
		})
	}
	if codeOf(t, svc.checkSourceGuard(context.Background(), prev, next)) != CodeSourceGuardTooManyClasses {
		t.Fatal("65 priced classes were accepted")
	}
	next.Relays = next.Relays[:domain.MaxPricedClasses]
	if err := svc.checkSourceGuard(context.Background(), prev, next); err != nil {
		t.Fatalf("64 classes: %v", err)
	}
	var many []string
	for i := 0; i < domain.MaxSourceCIDRs; i++ {
		many = append(many, fmt.Sprintf("10.%d.%d.0/24", i/256, i%256))
	}
	big := &domain.Node{ID: 1, PanelID: 10, DisplayName: "hk", Relays: []domain.RelayLine{
		{ID: "a", Name: "a", Address: "a", Enabled: true, SourceCIDRs: many, TrafficFactorMilli: milli(2000)},
		{ID: "b", Name: "b", Address: "b", Enabled: true, SourceCIDRs: []string{"172.16.0.0/16"}, TrafficFactorMilli: milli(2000)},
	}}
	if codeOf(t, svc.checkSourceGuard(context.Background(), prev, big)) != CodeSourceGuardTooManyCIDRs {
		t.Fatal("a class over 1024 addresses was accepted")
	}
}

// A listener whose core reads the source from a client-sent header cannot
// price lines apart even on a capable server, and the refusal says why.
func TestSourceGuardTreatsAHeaderTrustingListenerAsUnableToGuard(t *testing.T) {
	svc := guardService(&factorNodes{}, true)
	landing := &domain.Node{ID: 1, PanelID: 10, DisplayName: "hk",
		StreamSettings: `{"network":"ws","sockopt":{"trustedXForwardedFor":["X-Forwarded-For"]}}`,
		Relays:         []domain.RelayLine{cdnLine("a", 2000)}}
	err := svc.checkSourceGuard(context.Background(), &domain.Node{ID: 1, PanelID: 10}, landing)
	if codeOf(t, err) != CodeSourceGuardFactorMismatch || !strings.Contains(err.Error(), "X-Forwarded-For") {
		t.Fatalf("err = %v", err)
	}
}

// Imported lines are held to the editor's rules.
func TestImportExistingNormalisesAndRefusesSourceAddresses(t *testing.T) {
	inb := &ports.Inbound{ID: 3, Port: 443, Protocol: "vless", StreamSettings: `{"network":"tcp"}`, Settings: `{"clients":[],"decryption":"none"}`}
	for _, bad := range []string{"198.18.0.1/24", "0.0.0.0/0", "::ffff:10.0.0.1"} {
		repo := &captureNodeRepo{}
		svc := &Service{nodes: repo, pool: stubXUIPool{c: &stubXUIClient{getResp: inb}}, groups: emptyGroups{}}
		n := &domain.Node{DisplayName: "Tokyo", Region: "JP", PanelID: 1, InboundID: 3,
			Relays: []domain.RelayLine{{Name: "x", Address: "x.example", Port: 1, Enabled: true, SourceCIDRs: []string{bad}}}}
		if err := svc.ImportExisting(context.Background(), n); !errors.Is(err, domain.ErrValidation) || repo.created != nil {
			t.Errorf("%s: err %v created %v", bad, err, repo.created)
		}
	}
	repo := &captureNodeRepo{}
	svc := &Service{nodes: repo, pool: stubXUIPool{c: &stubXUIClient{getResp: inb}}, groups: emptyGroups{}}
	n := &domain.Node{DisplayName: "Tokyo", Region: "JP", PanelID: 1, InboundID: 3,
		Relays: []domain.RelayLine{{Name: "x", Address: "x.example", Port: 1, Enabled: true, SourceCIDRs: []string{" 203.0.113.9 "}}}}
	if err := svc.ImportExisting(context.Background(), n); err != nil || repo.created == nil {
		t.Fatalf("a good address: %v", err)
	}
	if got := repo.created.Relays[0].SourceCIDRs; !reflect.DeepEqual(got, []string{"203.0.113.9/32"}) || repo.created.Relays[0].ID == "" {
		t.Fatalf("stored line = %+v", repo.created.Relays[0])
	}
}

// An import that would put lines at prices a landing cannot keep apart is
// refused like a save.
func TestImportExistingAppliesTheFactorRules(t *testing.T) {
	inb := &ports.Inbound{ID: 3, Port: 443, Protocol: "vless", StreamSettings: `{"network":"tcp"}`, Settings: `{"clients":[],"decryption":"none"}`}
	repo := &captureNodeRepo{}
	svc := &Service{nodes: repo, pool: stubXUIPool{c: &stubXUIClient{getResp: inb}}, groups: emptyGroups{}}
	svc.SetSourceGuardCheck(func(context.Context, int64) bool { return false })
	n := &domain.Node{DisplayName: "Tokyo", Region: "JP", PanelID: 1, InboundID: 3,
		Relays: []domain.RelayLine{{Name: "x", Address: "x.example", Port: 1, Enabled: true, TrafficFactorMilli: milli(2000)}}}
	if err := svc.ImportExisting(context.Background(), n); codeOf(t, err) != CodeSourceGuardFactorMismatch || repo.created != nil {
		t.Fatalf("err %v created %v", err, repo.created)
	}
}

func listenerGuardService(repo *captureNodeRepo, capable bool, engines ...domain.NodeCoreEngine) *Service {
	svc := &Service{nodes: repo, pool: stubXUIPool{c: &stubXUIClient{}}}
	svc.SetSourceGuardInputs(func(_ context.Context, nodes []*domain.Node) (domain.SourceInputs, error) {
		in := domain.SourceInputs{Cloudflare: domain.BuiltinCloudflare, Servers: map[int64]domain.SourceGuardServer{}}
		for _, n := range nodes {
			in.Servers[n.PanelID] = domain.SourceGuardServer{Capable: capable, Engines: engines}
		}
		return in, nil
	})
	return svc
}

// A listener edit that stops a guarding listener from guarding is refused while
// lines at another factor end on it.
func TestUpdateInboundConfigRefusesWhatStopsAListenerFromGuarding(t *testing.T) {
	priced := []domain.RelayLine{{ID: "p", Name: "Priced", Address: "r.example", Port: 1, Enabled: true, SourceCIDRs: []string{"192.0.2.0/24"}, TrafficFactorMilli: milli(2000)}}
	spec := func(stream string) ports.InboundSpec {
		return ports.InboundSpec{Protocol: "vless", Port: 443, Settings: `{"clients":[]}`, StreamSettings: stream}
	}
	for _, c := range []struct {
		name    string
		engines []domain.NodeCoreEngine
		from    string
		to      string
		lines   []domain.RelayLine
		code    string // "" means accepted
	}{
		{"xray trusts a header", nil, `{"network":"tcp"}`, `{"network":"tcp","Sockopt":{"TrustedXForwardedFor":["X-Forwarded-For"]}}`, priced, CodeSourceGuardListenerTrust},
		{"xray accepts proxy protocol", nil, `{"network":"tcp"}`, `{"network":"tcp","tcpSettings":{"AcceptProxyProtocol":true}}`, priced, CodeSourceGuardListenerTrust},
		{"sing-box moves to ws", []domain.NodeCoreEngine{domain.NodeCoreSingBox}, `{"network":"tcp"}`, `{"network":"ws"}`, priced, CodeSourceGuardListenerTransport},
		{"no priced lines", nil, `{"network":"tcp"}`, `{"network":"tcp","sockopt":{"trustedXForwardedFor":["X"]}}`, []domain.RelayLine{{ID: "d", Address: "r.example", Port: 1, Enabled: true}}, ""},
		{"already not guardable", nil, `{"sockopt":{"trustedXForwardedFor":["X"]}}`, `{"sockopt":{"trustedXForwardedFor":["Y"]}}`, priced, ""},
		{"harmless edit", nil, `{"network":"tcp"}`, `{"network":"tcp","security":"tls"}`, priced, ""},
		{"xray ws stays guardable", nil, `{"network":"tcp"}`, `{"network":"ws"}`, priced, ""},
	} {
		n := cdnLanding(c.from)
		n.Relays = c.lines
		repo := &captureNodeRepo{node: &n}
		err := listenerGuardService(repo, true, c.engines...).UpdateInboundConfig(context.Background(), 1, spec(c.to))
		if c.code == "" {
			if err != nil {
				t.Errorf("%s: %v", c.name, err)
			}
			continue
		}
		var coded *domain.CodedValidationError
		if !errors.As(err, &coded) || coded.Code != c.code || !strings.Contains(coded.Params["lines"], "Priced") || repo.updateCfg != nil {
			t.Errorf("%s: err = %v written %v", c.name, err, repo.updateCfg)
		}
	}
	// A server that cannot guard is not asked: its priced lines are already refused.
	n := cdnLanding(`{"network":"tcp"}`)
	n.Relays = priced
	repo := &captureNodeRepo{node: &n}
	err := listenerGuardService(repo, false).UpdateInboundConfig(context.Background(), 1, spec(`{"sockopt":{"trustedXForwardedFor":["X"]}}`))
	if err != nil {
		t.Fatalf("incapable server: %v", err)
	}
}
