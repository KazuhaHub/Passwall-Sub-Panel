package domain

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"testing"
)

// enumProtocols parses enums.go and returns every constant declared with type
// Protocol, so the exhaustiveness check below is derived from the enum source
// itself rather than from the hand-maintained AllProtocols().
func enumProtocols(t *testing.T) []Protocol {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "enums.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []Protocol
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs := spec.(*ast.ValueSpec)
			id, ok := vs.Type.(*ast.Ident)
			if !ok || id.Name != "Protocol" {
				continue
			}
			for _, v := range vs.Values {
				if lit, ok := v.(*ast.BasicLit); ok {
					out = append(out, Protocol(lit.Value[1:len(lit.Value)-1]))
				}
			}
		}
	}
	return out
}

func TestAllProtocolsMatchesEnumSource(t *testing.T) {
	src := enumProtocols(t)
	if len(src) == 0 {
		t.Fatal("parsed no Protocol constants")
	}
	got := AllProtocols()
	sort.Slice(src, func(i, j int) bool { return src[i] < src[j] })
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
	if len(src) != len(got) {
		t.Fatalf("enums.go declares %v, AllProtocols() returns %v", src, got)
	}
	for i := range src {
		if src[i] != got[i] {
			t.Fatalf("enums.go declares %v, AllProtocols() returns %v", src, got)
		}
	}
}

// Every protocol in the enum SOURCE needs an explicit credential decision.
func TestProtocolCredentialFields(t *testing.T) {
	want := map[Protocol]CredentialFields{
		ProtoVLESS: {ID: true},
		ProtoVMess: {ID: true},
		// Hysteria2 and TUIC are rendered from AppliedUUID (the stored id), so a
		// stale id must stay loud even though the protocol also reads auth /
		// password.
		ProtoHysteria2: {ID: true, Auth: true},
		ProtoTUIC:      {ID: true, Password: true},
		ProtoTrojan:    {Password: true},
		ProtoSS:        {Password: true},
		ProtoSS2022:    {Password: true},
		ProtoAnyTLS:    {Password: true},
		ProtoNaive:     {Password: true},
	}
	for _, p := range enumProtocols(t) {
		w, ok := want[p]
		if !ok {
			t.Fatalf("protocol %q has no credential decision in this table", p)
		}
		if got := CredentialFieldsFor(p); got != w {
			t.Errorf("CredentialFieldsFor(%s) = %+v, want %+v", p, got, w)
		}
		if got := p.UsesPassword(); got != w.Password {
			t.Errorf("%s.UsesPassword() = %v, want %v", p, got, w.Password)
		}
	}
}

func TestCredentialFieldsForUnionAndFailClosed(t *testing.T) {
	all := CredentialFields{ID: true, Password: true, Auth: true}
	if got := CredentialFieldsFor(ProtoVLESS, ProtoSS2022); got != (CredentialFields{ID: true, Password: true}) {
		t.Errorf("union = %+v", got)
	}
	if got := CredentialFieldsFor(); got != all {
		t.Errorf("empty = %+v, want all", got)
	}
	if got := CredentialFieldsFor(ProtoVLESS, Protocol("")); got != all {
		t.Errorf("unknown member = %+v, want all", got)
	}
	if got := CredentialFieldsFor(Protocol("wireguard")); got != all {
		t.Errorf("unknown = %+v, want all", got)
	}
}

func TestNodeCredentialProtocol(t *testing.T) {
	ss2022 := `{"method":"2022-blake3-aes-128-gcm","password":"x"}`
	cases := []struct {
		name, proto, settings string
		want                  Protocol
	}{
		{"shadowsocks_legacy", "shadowsocks", `{"method":"aes-256-gcm"}`, ProtoSS},
		{"ss_alias", "ss", `{"method":"aes-256-gcm"}`, ProtoSS},
		{"shadowsocks_2022", "shadowsocks", ss2022, ProtoSS2022},
		{"ss_alias_2022", "SS", ss2022, ProtoSS2022},
		{"hy2_alias", "hy2", "", ProtoHysteria2},
		{"hysteria2", "Hysteria2", "", ProtoHysteria2},
		{"vless", " vless ", "", ProtoVLESS},
		{"unknown", "wireguard", "", ""},
		{"empty", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := &Node{DesiredProtocol: tc.proto, InboundSettings: tc.settings}
			if got := n.CredentialProtocol(); got != tc.want {
				t.Fatalf("CredentialProtocol() = %q, want %q", got, tc.want)
			}
		})
	}
}
