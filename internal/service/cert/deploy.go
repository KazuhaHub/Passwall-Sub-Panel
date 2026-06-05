package cert

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
)

// errNotTLS marks a deploy target whose inbound isn't TLS (reality / none),
// where a managed CA certificate doesn't apply. The service skips such a bound
// node with a clear reason rather than treating it as a hard failure.
var errNotTLS = errors.New("inbound is not a TLS inbound")

// injectInlineCert rewrites an inbound's streamSettings so its
// tlsSettings.certificates carries a single INLINE cert entry — the exact shape
// PSP's own node form emits (web-react buildTLSSettings): certificate/key are
// single-element PEM-string arrays. It errors with errNotTLS when the inbound's
// security isn't "tls". All other tlsSettings fields (serverName/alpn/...) are
// preserved.
func injectInlineCert(streamSettings, certPEM, keyPEM string) (string, error) {
	var ss map[string]any
	if err := json.Unmarshal([]byte(streamSettings), &ss); err != nil {
		return "", fmt.Errorf("parse stream settings: %w", err)
	}
	if sec, _ := ss["security"].(string); sec != "tls" {
		return "", fmt.Errorf("%w (security=%q)", errNotTLS, ss["security"])
	}
	tls, _ := ss["tlsSettings"].(map[string]any)
	if tls == nil {
		tls = map[string]any{}
	}
	tls["certificates"] = []any{map[string]any{
		"certificate":    []string{certPEM},
		"key":            []string{keyPEM},
		"ocspStapling":   3600,
		"oneTimeLoading": false,
		"usage":          "encipherment",
		"buildChain":     false,
	}}
	ss["tlsSettings"] = tls
	out, err := json.Marshal(ss)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// extractInlineCertPEM returns the first inline certificate PEM currently in an
// inbound's streamSettings (tlsSettings.certificates[0].certificate[0]), or ""
// when none. Drives the content-diff deploy gate.
func extractInlineCertPEM(streamSettings string) string {
	var ss map[string]any
	if json.Unmarshal([]byte(streamSettings), &ss) != nil {
		return ""
	}
	tls, _ := ss["tlsSettings"].(map[string]any)
	if tls == nil {
		return ""
	}
	certs, _ := tls["certificates"].([]any)
	if len(certs) == 0 {
		return ""
	}
	c, _ := certs[0].(map[string]any)
	if c == nil {
		return ""
	}
	arr, _ := c["certificate"].([]any)
	if len(arr) == 0 {
		return ""
	}
	pem, _ := arr[0].(string)
	return pem
}

// DeployToNode writes the certificate inline into the node's StreamSettings and
// enqueues a retryable config push (NodeConfigPusher). It's content-diff-gated:
// when the exact cert is already inlined it returns nil without a push (no
// needless Xray restart). errNotTLS for a non-TLS inbound surfaces to the
// caller, which logs a skip rather than failing.
func (s *Service) DeployToNode(ctx context.Context, n *domain.Node, cert *domain.TLSCertificate) error {
	// Re-read the node so the content-diff gate and the persisted update both
	// operate on current DB truth, not a snapshot captured earlier in the cert
	// task. Otherwise a concurrent admin edit between load and deploy could make
	// the gate skip a needed push (false "already deployed") or clobber a fresher
	// config.
	fresh, err := s.nodes.GetByID(ctx, n.ID)
	if err != nil {
		return err
	}
	n = fresh
	newSS, err := injectInlineCert(n.StreamSettings, cert.CertPEM, cert.KeyPEM)
	if err != nil {
		return err
	}
	if cert.CertPEM != "" && extractInlineCertPEM(n.StreamSettings) == cert.CertPEM {
		return nil // already deployed this exact cert — skip the restart
	}
	now := time.Now()
	n.StreamSettings = newSS
	n.ConfigSyncState = "pending"
	n.ConfigSyncedAt = &now
	if err := s.nodes.UpdateInboundConfig(ctx, n); err != nil {
		return err
	}
	return s.pusher.EnqueueConfigPush(ctx, n.ID)
}

func (s *Service) deployToBoundNodes(ctx context.Context, cert *domain.TLSCertificate) error {
	nodes, err := s.nodes.ListByCertID(ctx, cert.ID)
	if err != nil {
		return err
	}
	var firstErr error
	for _, n := range nodes {
		if err := s.DeployToNode(ctx, n, cert); err != nil {
			if errors.Is(err, errNotTLS) {
				log.Warn("cert deploy skipped: node inbound is not TLS", "cert_id", cert.ID, "node_id", n.ID)
				continue
			}
			log.Warn("cert deploy to node failed", "cert_id", cert.ID, "node_id", n.ID, "err", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}
