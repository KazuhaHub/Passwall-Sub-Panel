package handler

import (
	"errors"
	"fmt"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/samlguard"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/auth"
)

// samlFailureReason decides what an ACS refusal is recorded as. It labels a
// metric and lands in auth_events.reason, so its answer has to be a closed set
// of codes rather than whatever string an error happens to carry.
func TestSAMLFailureReason(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"replay", auth.ErrSAMLAssertionReplayed, samlReasonReplayed},
		{"replay wrapped by the service", fmt.Errorf("SAML response rejected: %w", auth.ErrSAMLAssertionReplayed), samlReasonReplayed},
		{"replay store outage", fmt.Errorf("%w: replay store: connection refused", domain.ErrUnavailable), samlReasonStoreError},
		{"weak signature", fmt.Errorf("SAML response rejected: %w", samlguard.ErrWeakSignatureAlgorithm), samlReasonWeakSignature},
		{"multiple assertions", samlguard.ErrTooManyAssertions, samlReasonMultiAssertion},
		{"missing destination", samlguard.ErrMissingDestination, samlReasonDestination},
		{"destination mismatch", samlguard.ErrDestinationMismatch, samlReasonDestination},
		{"malformed response", samlguard.ErrMalformed, samlReasonAssertionInvalid},
		{"login request invalid", domain.ErrSAMLRequestInvalid, samlReasonRequestInvalid},
		{"signature failure from crewjam", errors.New("parse SAML response: signature invalid"), samlReasonAssertionInvalid},
		{"nil never panics", nil, samlReasonAssertionInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := samlFailureReason(c.err); got != c.want {
				t.Fatalf("samlFailureReason(%v) = %q, want %q", c.err, got, c.want)
			}
		})
	}
}

// The separation that earns its keep: a replay is an attack to investigate, a
// replay-store failure is an outage to fix. Giving them one code — which is what
// the single "saml_assertion_invalid" they used to share did — means an operator
// eventually stops trusting the word "replay".
func TestSAMLFailureReasonSeparatesReplayFromOutage(t *testing.T) {
	replay := samlFailureReason(auth.ErrSAMLAssertionReplayed)
	outage := samlFailureReason(fmt.Errorf("%w: replay store", domain.ErrUnavailable))
	if replay == outage {
		t.Fatalf("a replay and a store outage both report %q; the two would be indistinguishable in the metric and the audit trail", replay)
	}
}
