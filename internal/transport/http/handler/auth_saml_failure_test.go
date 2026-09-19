package handler

import (
	"errors"
	"fmt"
	"strings"
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
		{"wrong entry point", auth.ErrSAMLEntryPoint, samlReasonEntryPoint},
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

// The failure page is the one place the reason codes become user-visible, and
// what a user can act on is coarser than what an operator needs. Eight
// server-side reasons collapse to three page codes: retry, an IdP configuration
// to fix, or a plain refusal. The fine-grained reason still reaches the metric
// and the audit row — it is the PAGE that must stay low-cardinality, because
// every code here needs copy in two languages and a code an admin has to look up
// is worse than a sentence.
func TestSAMLFailurePageCode(t *testing.T) {
	cases := map[string]struct {
		err  error
		want string
	}{
		"replay":                 {auth.ErrSAMLAssertionReplayed, samlPageAuthFailed},
		"assertion invalid":      {samlguard.ErrMalformed, samlPageAuthFailed},
		"request invalid":        {domain.ErrSAMLRequestInvalid, samlPageAuthFailed},
		"entry point":            {auth.ErrSAMLEntryPoint, samlPageAuthFailed},
		"signature from crewjam": {errors.New("signature invalid"), samlPageAuthFailed},

		"weak signature":      {samlguard.ErrWeakSignatureAlgorithm, samlPageConfig},
		"multiple assertions": {samlguard.ErrTooManyAssertions, samlPageConfig},
		"destination":         {samlguard.ErrMissingDestination, samlPageConfig},

		"store outage": {fmt.Errorf("%w: replay store", domain.ErrUnavailable), samlPageUnavailable},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := samlFailurePageCode(c.err); got != c.want {
				t.Fatalf("samlFailurePageCode(%v) = %q, want %q", c.err, got, c.want)
			}
		})
	}
}

// The page code must be a closed set: it decides which localized string a
// signed-out browser sees, so it cannot be anything an error happens to carry.
func TestSAMLFailurePageCodeIsAClosedSet(t *testing.T) {
	allowed := map[string]bool{samlPageAuthFailed: true, samlPageConfig: true, samlPageUnavailable: true}
	for _, err := range []error{
		nil, errors.New("anything"), auth.ErrSAMLAssertionReplayed, domain.ErrSAMLRequestInvalid,
		samlguard.ErrWeakSignatureAlgorithm, samlguard.ErrTooManyAssertions, samlguard.ErrDestinationMismatch,
		auth.ErrSAMLEntryPoint, fmt.Errorf("%w: x", domain.ErrUnavailable),
	} {
		if got := samlFailurePageCode(err); !allowed[got] {
			t.Fatalf("samlFailurePageCode(%v) = %q, which is outside the closed set", err, got)
		}
	}
}

// A refusal must not put free text in the URL. It is rendered by the SPA (so
// anything an IdP chose to say would be reflected into the page), it survives in
// browser history and in Referer headers, and it overrides the localized copy —
// which is the reason the account-state codes already carry no description.
func TestSAMLFailureRedirectCarriesNoFreeText(t *testing.T) {
	for _, err := range []error{
		auth.ErrSAMLAssertionReplayed,
		fmt.Errorf("%w: replay store", domain.ErrUnavailable),
		samlguard.ErrWeakSignatureAlgorithm,
		errors.New("IdP said: this account is not licensed for the application"),
	} {
		got := samlFailureRedirect(err)
		if strings.Contains(got, "description=") {
			t.Fatalf("the redirect for %v carries a description: %q", err, got)
		}
		if !strings.Contains(got, "error="+samlFailurePageCode(err)) {
			t.Fatalf("the redirect for %v does not carry its page code: %q", err, got)
		}
		// The IdP's own words must not travel through the URL.
		if strings.Contains(got, "licensed") || strings.Contains(got, "IdP") {
			t.Fatalf("free text survived into the redirect: %q", got)
		}
	}
}
