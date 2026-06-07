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
	"github.com/go-acme/lego/v4/providers/dns/bunny"
	"github.com/go-acme/lego/v4/providers/dns/cloudflare"
	"github.com/go-acme/lego/v4/providers/dns/cloudns"
	"github.com/go-acme/lego/v4/providers/dns/desec"
	"github.com/go-acme/lego/v4/providers/dns/digitalocean"
	"github.com/go-acme/lego/v4/providers/dns/dnsimple"
	"github.com/go-acme/lego/v4/providers/dns/dnspod"
	"github.com/go-acme/lego/v4/providers/dns/duckdns"
	"github.com/go-acme/lego/v4/providers/dns/dynu"
	"github.com/go-acme/lego/v4/providers/dns/exec"
	"github.com/go-acme/lego/v4/providers/dns/gandiv5"
	"github.com/go-acme/lego/v4/providers/dns/gcloud"
	"github.com/go-acme/lego/v4/providers/dns/godaddy"
	"github.com/go-acme/lego/v4/providers/dns/hetzner"
	"github.com/go-acme/lego/v4/providers/dns/httpreq"
	"github.com/go-acme/lego/v4/providers/dns/linode"
	"github.com/go-acme/lego/v4/providers/dns/namecheap"
	"github.com/go-acme/lego/v4/providers/dns/namedotcom"
	"github.com/go-acme/lego/v4/providers/dns/namesilo"
	"github.com/go-acme/lego/v4/providers/dns/netcup"
	"github.com/go-acme/lego/v4/providers/dns/njalla"
	"github.com/go-acme/lego/v4/providers/dns/ovh"
	"github.com/go-acme/lego/v4/providers/dns/porkbun"
	"github.com/go-acme/lego/v4/providers/dns/regru"
	"github.com/go-acme/lego/v4/providers/dns/route53"
	"github.com/go-acme/lego/v4/providers/dns/vercel"
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
	"dnsimple":     func() (challenge.Provider, error) { return dnsimple.NewDNSProvider() },
	"bunny":        func() (challenge.Provider, error) { return bunny.NewDNSProvider() },
	"cloudns":      func() (challenge.Provider, error) { return cloudns.NewDNSProvider() },
	"dynu":         func() (challenge.Provider, error) { return dynu.NewDNSProvider() },
	"netcup":       func() (challenge.Provider, error) { return netcup.NewDNSProvider() },
	"njalla":       func() (challenge.Provider, error) { return njalla.NewDNSProvider() },
	"vercel":       func() (challenge.Provider, error) { return vercel.NewDNSProvider() },
	"namedotcom":   func() (challenge.Provider, error) { return namedotcom.NewDNSProvider() },
	"regru":        func() (challenge.Provider, error) { return regru.NewDNSProvider() },
	"exec":         func() (challenge.Provider, error) { return exec.NewDNSProvider() },
	"httpreq":      func() (challenge.Provider, error) { return httpreq.NewDNSProvider() },
}

// DNSProviderField is one credential env var a curated provider needs. Key is the
// EXACT env var lego reads (mirrored from the provider's lego .toml Credentials
// block) — it's injected verbatim as a process env var at issuance. Secret marks
// values to mask in the UI and treat as write-only on edit.
type DNSProviderField struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Secret   bool   `json:"secret"`
	Optional bool   `json:"optional,omitempty"`
}

// DNSProviderInfo is one entry of the catalog the admin UI renders. For a curated
// vendor, Fields lists exactly the inputs to collect (labeled, no guessing). For
// the generic fallbacks (exec/httpreq) Custom=true tells the UI to fall back to
// the free-form KEY/VALUE editor.
type DNSProviderInfo struct {
	Name   string             `json:"name"`
	Label  string             `json:"label"`
	Custom bool               `json:"custom"`
	Fields []DNSProviderField `json:"fields,omitempty"`
}

// providerMeta is the credential field schema per curated provider, mirrored from
// each provider's lego .toml [Configuration.Credentials] block (so the env keys
// match exactly what lego reads). EVERY non-generic provider in providerFactories
// MUST have an entry here — enforced by TestProviderMetaCoversFactories — so a
// built-in vendor never degrades to the raw KV editor. exec/httpreq are
// deliberately absent (Custom=true is synthesized for them).
var providerMeta = map[string]DNSProviderInfo{
	"cloudflare": {Label: "Cloudflare", Fields: []DNSProviderField{
		{Key: "CF_DNS_API_TOKEN", Label: "API Token (DNS:Edit + Zone:Read)", Secret: true},
		{Key: "CF_ZONE_API_TOKEN", Label: "Zone API Token (only if scoped separately)", Secret: true, Optional: true},
	}},
	"alidns": {Label: "Alibaba Cloud DNS", Fields: []DNSProviderField{
		{Key: "ALICLOUD_ACCESS_KEY", Label: "Access Key ID"},
		{Key: "ALICLOUD_SECRET_KEY", Label: "Access Key Secret", Secret: true},
	}},
	"dnspod": {Label: "DNSPod", Fields: []DNSProviderField{
		{Key: "DNSPOD_API_KEY", Label: "API Token", Secret: true},
	}},
	"route53": {Label: "AWS Route 53", Fields: []DNSProviderField{
		{Key: "AWS_ACCESS_KEY_ID", Label: "Access Key ID"},
		{Key: "AWS_SECRET_ACCESS_KEY", Label: "Secret Access Key", Secret: true},
		{Key: "AWS_REGION", Label: "Region (e.g. us-east-1)"},
		{Key: "AWS_HOSTED_ZONE_ID", Label: "Hosted Zone ID", Optional: true},
	}},
	"gcloud": {Label: "Google Cloud DNS", Fields: []DNSProviderField{
		{Key: "GCE_PROJECT", Label: "Project ID"},
		{Key: "GCE_SERVICE_ACCOUNT", Label: "Service Account JSON", Secret: true},
	}},
	"azuredns": {Label: "Azure DNS", Fields: []DNSProviderField{
		{Key: "AZURE_CLIENT_ID", Label: "Client ID"},
		{Key: "AZURE_CLIENT_SECRET", Label: "Client Secret", Secret: true},
		{Key: "AZURE_TENANT_ID", Label: "Tenant ID"},
	}},
	"godaddy": {Label: "GoDaddy", Fields: []DNSProviderField{
		{Key: "GODADDY_API_KEY", Label: "API Key"},
		{Key: "GODADDY_API_SECRET", Label: "API Secret", Secret: true},
	}},
	"namecheap": {Label: "Namecheap", Fields: []DNSProviderField{
		{Key: "NAMECHEAP_API_USER", Label: "API User"},
		{Key: "NAMECHEAP_API_KEY", Label: "API Key", Secret: true},
	}},
	"namesilo": {Label: "NameSilo", Fields: []DNSProviderField{
		{Key: "NAMESILO_API_KEY", Label: "API Key", Secret: true},
	}},
	"porkbun": {Label: "Porkbun", Fields: []DNSProviderField{
		{Key: "PORKBUN_API_KEY", Label: "API Key"},
		{Key: "PORKBUN_SECRET_API_KEY", Label: "Secret API Key", Secret: true},
	}},
	"digitalocean": {Label: "DigitalOcean", Fields: []DNSProviderField{
		{Key: "DO_AUTH_TOKEN", Label: "API Token", Secret: true},
	}},
	"gandiv5": {Label: "Gandi", Fields: []DNSProviderField{
		{Key: "GANDIV5_PERSONAL_ACCESS_TOKEN", Label: "Personal Access Token", Secret: true},
	}},
	"hetzner": {Label: "Hetzner", Fields: []DNSProviderField{
		{Key: "HETZNER_API_TOKEN", Label: "API Token", Secret: true},
	}},
	"linode": {Label: "Linode", Fields: []DNSProviderField{
		{Key: "LINODE_TOKEN", Label: "API Token", Secret: true},
	}},
	"ovh": {Label: "OVH", Fields: []DNSProviderField{
		{Key: "OVH_ENDPOINT", Label: "Endpoint (ovh-eu / ovh-ca / ...)"},
		{Key: "OVH_APPLICATION_KEY", Label: "Application Key"},
		{Key: "OVH_APPLICATION_SECRET", Label: "Application Secret", Secret: true},
		{Key: "OVH_CONSUMER_KEY", Label: "Consumer Key", Secret: true},
	}},
	"vultr": {Label: "Vultr", Fields: []DNSProviderField{
		{Key: "VULTR_API_KEY", Label: "API Key", Secret: true},
	}},
	"desec": {Label: "deSEC", Fields: []DNSProviderField{
		{Key: "DESEC_TOKEN", Label: "Token", Secret: true},
	}},
	"duckdns": {Label: "DuckDNS", Fields: []DNSProviderField{
		{Key: "DUCKDNS_TOKEN", Label: "Token", Secret: true},
	}},
	"dnsimple": {Label: "DNSimple", Fields: []DNSProviderField{
		{Key: "DNSIMPLE_OAUTH_TOKEN", Label: "OAuth Token", Secret: true},
	}},
	"bunny": {Label: "Bunny.net", Fields: []DNSProviderField{
		{Key: "BUNNY_API_KEY", Label: "API Key", Secret: true},
	}},
	"cloudns": {Label: "ClouDNS", Fields: []DNSProviderField{
		{Key: "CLOUDNS_AUTH_ID", Label: "Auth ID"},
		{Key: "CLOUDNS_AUTH_PASSWORD", Label: "Auth Password", Secret: true},
	}},
	"dynu": {Label: "Dynu", Fields: []DNSProviderField{
		{Key: "DYNU_API_KEY", Label: "API Key", Secret: true},
	}},
	"netcup": {Label: "netcup", Fields: []DNSProviderField{
		{Key: "NETCUP_CUSTOMER_NUMBER", Label: "Customer Number"},
		{Key: "NETCUP_API_KEY", Label: "API Key"},
		{Key: "NETCUP_API_PASSWORD", Label: "API Password", Secret: true},
	}},
	"njalla": {Label: "Njalla", Fields: []DNSProviderField{
		{Key: "NJALLA_TOKEN", Label: "API Token", Secret: true},
	}},
	"vercel": {Label: "Vercel", Fields: []DNSProviderField{
		{Key: "VERCEL_API_TOKEN", Label: "API Token", Secret: true},
	}},
	"namedotcom": {Label: "Name.com", Fields: []DNSProviderField{
		{Key: "NAMECOM_USERNAME", Label: "Username"},
		{Key: "NAMECOM_API_TOKEN", Label: "API Token", Secret: true},
	}},
	"regru": {Label: "reg.ru", Fields: []DNSProviderField{
		{Key: "REGRU_USERNAME", Label: "Username"},
		{Key: "REGRU_PASSWORD", Label: "Password", Secret: true},
	}},
}

// genericProviderLabels gives the two SDK-free long-tail fallbacks a friendly
// name; they carry no fixed schema (Custom=true → free-form KEY/VALUE editor).
var genericProviderLabels = map[string]string{
	"exec":    "Custom — exec script",
	"httpreq": "Custom — HTTP webhook",
}

// SupportedProviderInfos returns the curated provider catalog (sorted by code)
// with each provider's credential field schema, so the admin UI can render
// labeled inputs instead of asking the operator to guess raw KEY=VALUE pairs.
// exec/httpreq surface as Custom=true.
func SupportedProviderInfos() []DNSProviderInfo {
	out := make([]DNSProviderInfo, 0, len(providerFactories))
	for code := range providerFactories {
		if meta, ok := providerMeta[code]; ok {
			meta.Name = code
			out = append(out, meta)
			continue
		}
		label := genericProviderLabels[code]
		if label == "" {
			label = code
		}
		out = append(out, DNSProviderInfo{Name: code, Label: label, Custom: true})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
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
	cfg.Certificate.KeyType = keyTypeFromString(req.KeyType)

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
		var r *registration.Resource
		var rerr error
		if req.EABKeyID != "" {
			// External Account Binding — ZeroSSL / Google Public CA and other
			// EAB-gated CAs require binding the new ACME account to a pre-issued
			// (kid, HMAC) credential at registration time.
			r, rerr = client.Registration.RegisterWithExternalAccountBinding(registration.RegisterEABOptions{
				TermsOfServiceAgreed: true,
				Kid:                  req.EABKeyID,
				HmacEncoded:          req.EABHMACKey,
			})
		} else {
			r, rerr = client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
		}
		if rerr != nil {
			return ports.ACMEResult{}, fmt.Errorf("acme: register account: %w", rerr)
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

// keyTypeFromString maps an account's configured key-type string to lego's
// certcrypto.KeyType for the issued certificate. Unknown/empty falls back to
// EC256 (the prior hard-coded default), so existing accounts are unaffected.
func keyTypeFromString(s string) certcrypto.KeyType {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "EC384":
		return certcrypto.EC384
	case "RSA2048":
		return certcrypto.RSA2048
	case "RSA4096":
		return certcrypto.RSA4096
	default:
		return certcrypto.EC256
	}
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
