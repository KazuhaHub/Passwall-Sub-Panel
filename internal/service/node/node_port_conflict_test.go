package node

import (
	"errors"
	"fmt"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// TestIsPortAlreadyExistsError covers BOTH wordings 3X-UI has used for an
// AddInbound port collision: the legacy "port already exists" and the
// transport-aware ">= 3.4.2" form "port <p> (<proto>) already used by inbound
// ..." (LIVE-observed on a real 3.5.0 panel). Both must classify as a port
// conflict so fail-fast + orphan adoption fire.
func TestIsPortAlreadyExistsError(t *testing.T) {
	// The 3.5.0 message the panel actually returns, as PSP receives it after the
	// client layer wraps a {success:false} body in domain.ErrValidation.
	live350 := fmt.Errorf("%w: [srv @ host] POST /panel/api/inbounds/add: "+
		"Something went wrong (port 443 (tcp) already used by inbound 'USA NY - Host' (#1) on *\n)",
		domain.ErrValidation)

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"unrelated", errors.New("dial tcp: connection refused"), false},
		{"legacy wording", errors.New("port already exists"), true},
		{"3.5.0 wording", errors.New("port 8443 (tcp) already used by inbound 'x' (#2) on *"), true},
		{"3.5.0 wording wrapped in ErrValidation", live350, true},
		{"case-insensitive", errors.New("ALREADY USED BY INBOUND"), true},
	}
	for _, tc := range cases {
		if got := isPortAlreadyExistsError(tc.err); got != tc.want {
			t.Errorf("%s: isPortAlreadyExistsError = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestPermanentInboundCreateError verifies a port conflict (either wording) is
// promoted to domain.ErrAlreadyExists so the node-create task runner fails fast
// instead of retrying a permanently-unsatisfiable create to the attempt cap;
// non-conflict errors return nil (stay transient/retryable).
func TestPermanentInboundCreateError(t *testing.T) {
	if got := permanentInboundCreateError(nil); got != nil {
		t.Fatalf("nil error must map to nil, got %v", got)
	}
	if got := permanentInboundCreateError(errors.New("temporary network blip")); got != nil {
		t.Fatalf("non-conflict error must stay transient (nil), got %v", got)
	}
	for _, msg := range []string{
		"port already exists",
		"Something went wrong (port 443 (tcp) already used by inbound 'USA NY - Host' (#1) on *)",
	} {
		got := permanentInboundCreateError(errors.New(msg))
		if got == nil || !errors.Is(got, domain.ErrAlreadyExists) {
			t.Fatalf("port conflict %q must map to ErrAlreadyExists, got %v", msg, got)
		}
	}
}
