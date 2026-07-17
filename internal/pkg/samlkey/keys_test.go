package samlkey

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"
)

func TestGenerateSelfSigned(t *testing.T) {
	for _, tt := range []struct {
		name       string
		commonName string
		wantCN     string
	}{
		{name: "custom entity ID", commonName: "https://panel.example/saml", wantCN: "https://panel.example/saml"},
		{name: "default common name", commonName: "", wantCN: "passwall-sub-panel-sp"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			certPEM, keyPEM, err := GenerateSelfSigned(tt.commonName)
			if err != nil {
				t.Fatal(err)
			}
			certBlock, rest := pem.Decode([]byte(certPEM))
			if certBlock == nil || certBlock.Type != "CERTIFICATE" || len(rest) != 0 {
				t.Fatal("certificate output is not a single CERTIFICATE PEM block")
			}
			cert, err := x509.ParseCertificate(certBlock.Bytes)
			if err != nil {
				t.Fatal(err)
			}
			keyBlock, rest := pem.Decode([]byte(keyPEM))
			if keyBlock == nil || keyBlock.Type != "RSA PRIVATE KEY" || len(rest) != 0 {
				t.Fatal("key output is not a single RSA PRIVATE KEY PEM block")
			}
			key, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
			if err != nil {
				t.Fatal(err)
			}
			if key.N.BitLen() != 2048 {
				t.Fatalf("RSA key size = %d, want 2048", key.N.BitLen())
			}
			certPublic, ok := cert.PublicKey.(*rsa.PublicKey)
			if !ok || certPublic.N.Cmp(key.N) != 0 || certPublic.E != key.E {
				t.Fatal("certificate public key does not match private key")
			}
			if cert.Subject.CommonName != tt.wantCN {
				t.Fatalf("common name = %q, want %q", cert.Subject.CommonName, tt.wantCN)
			}
			now := time.Now()
			if now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) {
				t.Fatalf("certificate validity %s..%s does not include now", cert.NotBefore, cert.NotAfter)
			}
			if cert.KeyUsage&x509.KeyUsageDigitalSignature == 0 || cert.KeyUsage&x509.KeyUsageKeyEncipherment == 0 {
				t.Fatalf("unexpected key usage: %v", cert.KeyUsage)
			}
			if err := cert.CheckSignature(cert.SignatureAlgorithm, cert.RawTBSCertificate, cert.Signature); err != nil {
				t.Fatalf("certificate is not self-signed: %v", err)
			}
		})
	}
}
