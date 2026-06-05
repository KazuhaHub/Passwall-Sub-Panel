// Package acme implements ports.ACMEIssuer on top of go-acme/lego. It is the
// ONLY place in the codebase that imports lego — domain/service/ports stay
// lego-free so the ACME engine can be swapped without touching business logic.
//
// DNS providers are CURATED (an explicit registry of common vendors) rather
// than lego's all-~150 `providers/dns` aggregator, which would pull every cloud
// SDK (AWS/Azure/GCP/Alibaba/...) into this single self-contained binary. The
// long tail is served by the generic "exec" (run a script) and "httpreq"
// (call a webhook) providers, which add no cloud SDK. Adding a vendor = one
// import + one registry line.
package acme

import (
	"context"
	"crypto"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"

	"github.com/go-acme/lego/v4/providers/dns/alidns"
	"github.com/go-acme/lego/v4/providers/dns/azuredns"
	"github.com/go-acme/lego/v4/providers/dns/cloudflare"
	"github.com/go-acme/lego/v4/providers/dns/desec"
	"github.com/go-acme/lego/v4/providers/dns/digitalocean"
	"github.com/go-acme/lego/v4/providers/dns/dnspod"
	"github.com/go-acme/lego/v4/providers/dns/duckdns"
	"github.com/go-acme/lego/v4/providers/dns/exec"
	"github.com/go-acme/lego/v4/providers/dns/gandiv5"
	"github.com/go-acme/lego/v4/providers/dns/gcloud"
	"github.com/go-acme/lego/v4/providers/dns/godaddy"
	"github.com/go-acme/lego/v4/providers/dns/hetzner"
	"github.com/go-acme/lego/v4/providers/dns/httpreq"
	"github.com/go-acme/lego/v4/providers/dns/linode"
	"github.com/go-acme/lego/v4/providers/dns/namecheap"
	"github.com/go-acme/lego/v4/providers/dns/namesilo"
	"github.com/go-acme/lego/v4/providers/dns/ovh"
	"github.com/go-acme/lego/v4/providers/dns/porkbun"
	"github.com/go-acme/lego/v4/providers/dns/route53"
	"github.com/go-acme/lego/v4/providers/dns/vultr"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// providerFactories maps a PSP/lego provider code to that provider's env-based
// constructor. The credentials map supplied per certificate is injected as the
// process env vars each constructor reads (their names are the lego-documented
// ones, e.g. CF_DNS_API_TOKEN); Obtain serializes so the env is never shared
// across concurrent issuances. "exec"/"httpreq" are the generic long-tail
// fallbacks (no cloud SDK).
var providerFactories = map[string]func() (challenge.Provider, error){
	"cloudflare":   func() (challenge.Provider, error) { return cloudflare.NewDNSProvider() },
	"alidns":       func() (challenge.Provider, error) { return alidns.NewDNSProvider() },
	"dnspod":       func() (challenge.Provider, error) { return dnspod.NewDNSProvider() },
	"route53":      func() (challenge.Provider, error) { return route53.NewDNSProvider() },
	"gcloud":       func() (challenge.Provider, error) { return gcloud.NewDNSProvider() },
	"azuredns":     func() (challenge.Provider, error) { return azuredns.NewDNSProvider() },
	"godaddy":      func() (challenge.Provider, error) { return godaddy.NewDNSProvider() },
	"namecheap":    func() (challenge.Provider, error) { return namecheap.NewDNSProvider() },
	"namesilo":     func() (challenge.Provider, error) { return namesilo.NewDNSProvider() },
	"porkbun":      func() (challenge.Provider, error) { return porkbun.NewDNSProvider() },
	"digitalocean": func() (challenge.Provider, error) { return digitalocean.NewDNSProvider() },
	"gandiv5":      func() (challenge.Provider, error) { return gandiv5.NewDNSProvider() },
	"hetzner":      func() (challenge.Provider, error) { return hetzner.NewDNSProvider() },
	"linode":       func() (challenge.Provider, error) { return linode.NewDNSProvider() },
	"ovh":          func() (challenge.Provider, error) { return ovh.NewDNSProvider() },
	"vultr":        func() (challenge.Provider, error) { return vultr.NewDNSProvider() },
	"desec":        func() (challenge.Provider, error) { return desec.NewDNSProvider() },
	"duckdns":      func() (challenge.Provider, error) { return duckdns.NewDNSProvider() },
	"exec":         func() (challenge.Provider, error) { return exec.NewDNSProvider() },
	"httpreq":      func() (challenge.Provider, error) { return httpreq.NewDNSProvider() },
}

// SupportedProviders returns the sorted list of built-in DNS provider codes.
// The HTTP layer surfaces it so the admin UI can populate a provider dropdown.
func SupportedProviders() []string {
	out := make([]string, 0, len(providerFactories))
	for k := range providerFactories {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Issuer is the lego-backed ACME issuer. Obtain is serialized by mu because the
// DNS provider constructors read PROCESS-LEVEL environment variables; running
// two issuances concurrently could bleed one cert's DNS credentials into
// another's. Issuance is low-frequency, so a global lock is cheap insurance.
type Issuer struct {
	mu sync.Mutex
}

func NewIssuer() *Issuer { return &Issuer{} }

var _ ports.ACMEIssuer = (*Issuer)(nil)

// acmeUser implements lego's registration.User.
type acmeUser struct {
	email string
	key   crypto.PrivateKey
	reg   *registration.Resource
}

func (u *acmeUser) GetEmail() string                        { return u.email }
func (u *acmeUser) GetRegistration() *registration.Resource { return u.reg }
func (u *acmeUser) GetPrivateKey() crypto.PrivateKey        { return u.key }

// Obtain runs the full DNS-01 issuance. ctx is accepted for interface symmetry;
// lego's Obtain has no context hook, so cancellation is best-effort at the
// boundaries rather than mid-challenge.
func (i *Issuer) Obtain(_ context.Context, req ports.ACMERequest) (ports.ACMEResult, error) {
	if len(req.Domains) == 0 {
		return ports.ACMEResult{}, fmt.Errorf("acme: no domains")
	}
	if req.DirectoryURL == "" {
		return ports.ACMEResult{}, fmt.Errorf("acme: directory URL required")
	}
	if req.DNSProvider == "" {
		return ports.ACMEResult{}, fmt.Errorf("acme: dns provider required")
	}

	key, accountKeyPEM, err := loadOrGenerateAccountKey(req.AccountKeyPEM)
	if err != nil {
		return ports.ACMEResult{}, err
	}

	var reg *registration.Resource
	if req.RegistrationJSON != "" {
		reg = &registration.Resource{}
		if err := json.Unmarshal([]byte(req.RegistrationJSON), reg); err != nil {
			return ports.ACMEResult{}, fmt.Errorf("acme: parse registration: %w", err)
		}
	}

	user := &acmeUser{email: req.Email, key: key, reg: reg}
	cfg := lego.NewConfig(user)
	cfg.CADirURL = req.DirectoryURL
	cfg.Certificate.KeyType = certcrypto.EC256

	client, err := lego.NewClient(cfg)
	if err != nil {
		return ports.ACMEResult{}, fmt.Errorf("acme: new client: %w", err)
	}

	// Serialize: the provider constructors read process env.
	i.mu.Lock()
	defer i.mu.Unlock()

	provider, cleanup, err := i.dnsProvider(req)
	if err != nil {
		return ports.ACMEResult{}, err
	}
	defer cleanup()

	if err := client.Challenge.SetDNS01Provider(provider); err != nil {
		return ports.ACMEResult{}, fmt.Errorf("acme: set dns-01 provider: %w", err)
	}

	regJSON := req.RegistrationJSON
	if user.reg == nil {
		r, err := client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
		if err != nil {
			return ports.ACMEResult{}, fmt.Errorf("acme: register account: %w", err)
		}
		user.reg = r
		b, err := json.Marshal(r)
		if err != nil {
			return ports.ACMEResult{}, fmt.Errorf("acme: marshal registration: %w", err)
		}
		regJSON = string(b)
	}

	res, err := client.Certificate.Obtain(certificate.ObtainRequest{Domains: req.Domains, Bundle: true})
	if err != nil {
		return ports.ACMEResult{}, fmt.Errorf("acme: obtain: %w", err)
	}

	notBefore, notAfter, fingerprint, err := leafInfo(res.Certificate)
	if err != nil {
		return ports.ACMEResult{}, err
	}

	return ports.ACMEResult{
		CertPEM:          string(res.Certificate),
		KeyPEM:           string(res.PrivateKey),
		AccountKeyPEM:    accountKeyPEM,
		RegistrationJSON: regJSON,
		NotBefore:        notBefore,
		NotAfter:         notAfter,
		Fingerprint:      fingerprint,
	}, nil
}

// dnsProvider looks up the curated factory, injects the caller's credentials as
// the env vars the constructor reads, and returns the provider plus a cleanup
// that unsets exactly those keys. MUST be called under i.mu.
func (i *Issuer) dnsProvider(req ports.ACMERequest) (challenge.Provider, func(), error) {
	factory, ok := providerFactories[req.DNSProvider]
	if !ok {
		return nil, nil, fmt.Errorf("acme: unsupported dns provider %q (built-in: %s; use \"exec\" or \"httpreq\" for others)",
			req.DNSProvider, strings.Join(SupportedProviders(), ", "))
	}
	set := make([]string, 0, len(req.DNSCredentials))
	for k, v := range req.DNSCredentials {
		_ = os.Setenv(k, v)
		set = append(set, k)
	}
	cleanup := func() {
		for _, k := range set {
			_ = os.Unsetenv(k)
		}
	}
	p, err := factory()
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("acme: build dns provider %q: %w", req.DNSProvider, err)
	}
	return p, cleanup, nil
}

// loadOrGenerateAccountKey parses an existing PEM account key, or generates a
// fresh EC256 one and returns its PEM so the caller can persist it. The PEM is
// echoed back unchanged when supplied so the account stays stable across renewals.
func loadOrGenerateAccountKey(pemKey string) (crypto.PrivateKey, string, error) {
	if pemKey != "" {
		key, err := certcrypto.ParsePEMPrivateKey([]byte(pemKey))
		if err != nil {
			return nil, "", fmt.Errorf("acme: parse account key: %w", err)
		}
		return key, pemKey, nil
	}
	key, err := certcrypto.GeneratePrivateKey(certcrypto.EC256)
	if err != nil {
		return nil, "", fmt.Errorf("acme: generate account key: %w", err)
	}
	return key, string(certcrypto.PEMEncode(key)), nil
}

// leafInfo extracts the validity window and a SHA-256 fingerprint (hex) of the
// leaf certificate from a PEM bundle. The fingerprint drives content-diff-gated
// redeploy so an unchanged renewal doesn't needlessly bounce a node's Xray.
func leafInfo(certPEM []byte) (notBefore, notAfter time.Time, fingerprint string, err error) {
	leaf, err := certcrypto.ParsePEMCertificate(certPEM)
	if err != nil {
		return time.Time{}, time.Time{}, "", fmt.Errorf("acme: parse leaf certificate: %w", err)
	}
	sum := sha256.Sum256(leaf.Raw)
	return leaf.NotBefore, leaf.NotAfter, hex.EncodeToString(sum[:]), nil
}
