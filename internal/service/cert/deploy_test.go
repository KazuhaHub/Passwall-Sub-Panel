package cert

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestInjectInlineCertSetsCertificatesArray(t *testing.T) {
	ss := `{"network":"tcp","security":"tls","tlsSettings":{"serverName":"x.example.com","alpn":["h2"]}}`
	out, err := injectInlineCert(ss, "CERTPEM", "KEYPEM")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatal(err)
	}
	tls := m["tlsSettings"].(map[string]any)
	// Other tlsSettings fields must survive the injection.
	if tls["serverName"] != "x.example.com" {
		t.Fatalf("serverName lost: %v", tls["serverName"])
	}
	certs := tls["certificates"].([]any)
	if len(certs) != 1 {
		t.Fatalf("want 1 certificate entry, got %d", len(certs))
	}
	c := certs[0].(map[string]any)
	certArr := c["certificate"].([]any)
	keyArr := c["key"].([]any)
	if len(certArr) != 1 || certArr[0] != "CERTPEM" {
		t.Fatalf("certificate = %v, want [\"CERTPEM\"]", certArr)
	}
	if len(keyArr) != 1 || keyArr[0] != "KEYPEM" {
		t.Fatalf("key = %v, want [\"KEYPEM\"]", keyArr)
	}
	// Inline-mode markers matching PSP's own buildTLSSettings output.
	if c["oneTimeLoading"] != false || c["usage"] != "encipherment" {
		t.Fatalf("inline markers missing: %#v", c)
	}
}

func TestInjectInlineCertRejectsNonTLS(t *testing.T) {
	for _, ss := range []string{
		`{"security":"reality","realitySettings":{}}`,
		`{"security":"none"}`,
		`{"network":"tcp"}`,
	} {
		if _, err := injectInlineCert(ss, "C", "K"); !errors.Is(err, errNotTLS) {
			t.Fatalf("non-tls (%s) must be errNotTLS, got %v", ss, err)
		}
	}
}

func TestInjectInlineCertCreatesTLSSettingsIfMissing(t *testing.T) {
	out, err := injectInlineCert(`{"security":"tls"}`, "C", "K")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatal(err)
	}
	tls, ok := m["tlsSettings"].(map[string]any)
	if !ok || tls["certificates"] == nil {
		t.Fatalf("certificates not set when tlsSettings was absent: %s", out)
	}
}

func TestInjectInlineCertRejectsBadJSON(t *testing.T) {
	if _, err := injectInlineCert("{not json", "C", "K"); err == nil {
		t.Fatal("malformed stream settings must error")
	}
}
