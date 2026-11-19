package domain

import "testing"

// Every Protocol value is listed so a protocol added to enums.go without a
// decision here fails the exhaustiveness check below instead of silently
// defaulting to "compare nothing" or "compare everything".
func TestProtocolCredentialFields(t *testing.T) {
	want := map[Protocol]CredentialFields{
		ProtoVLESS:     {ID: true},
		ProtoVMess:     {ID: true},
		ProtoTrojan:    {Password: true},
		ProtoSS:        {Password: true},
		ProtoSS2022:    {Password: true},
		ProtoAnyTLS:    {Password: true},
		ProtoTUIC:      {Password: true},
		ProtoNaive:     {Password: true},
		ProtoHysteria2: {Auth: true},
	}
	if len(want) != len(AllProtocols()) {
		t.Fatalf("test covers %d protocols, AllProtocols() has %d", len(want), len(AllProtocols()))
	}
	for _, p := range AllProtocols() {
		w, ok := want[p]
		if !ok {
			t.Fatalf("protocol %q missing from the table", p)
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
	// Nothing known, or a protocol we cannot classify: compare everything, so an
	// unrecognised attachment can never wave a credential mismatch through.
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
