package domain

import (
	"fmt"
	"net/netip"
	"strings"
	"time"
)

// Managed reports whether a Passwall Node server runs this line's forwarding.
func (r RelayLine) Managed() bool { return r.EntryPanelID != 0 }

// Publishable reports whether the line may appear in subscriptions, status
// pages and line references. An external line follows Enabled alone. A managed
// line also needs ManagedReady, so a line whose entry has not confirmed the
// forward (or whose state was never loaded) is withheld: it fails closed. A
// line whose probes have been down long enough to be withheld (ProbeWithheld,
// only ever set while withholding is on) is left out too, and never replaced
// by a direct line.
func (r RelayLine) Publishable() bool {
	return r.Enabled && !r.ProbeWithheld && (!r.Managed() || r.ManagedReady)
}

// ManagedForward is what an entry server must relay for one managed line. It
// is both the desired value PSP publishes and the applied value the entry
// confirms, so the two compare as plain values.
type ManagedForward struct {
	EntryPanelID int64
	Port         int
	Target       string
	TargetPort   int
}

// DesiredManagedForward is the forward a managed line asks for: its entry
// port, relayed to the landing node's server address and inbound port. ok is
// false when the line is not managed, is disabled, or its landing cannot be
// reached (disabled node, no address, no captured inbound port); such a line
// is not published to the entry either.
func DesiredManagedForward(landing *Node, line RelayLine) (ManagedForward, bool) {
	if landing == nil || !line.Managed() || !line.Enabled || !landing.Enabled || landing.Kind == NodeKindSeparator {
		return ManagedForward{}, false
	}
	target := strings.TrimSpace(landing.ServerAddress)
	if target == "" || landing.DesiredPort < 1 || landing.DesiredPort > 65535 || line.Port < 1 || line.Port > 65535 {
		return ManagedForward{}, false
	}
	if addr, err := netip.ParseAddr(strings.Trim(target, "[]")); err == nil {
		target = addr.Unmap().String()
	} else {
		target = strings.ToLower(strings.TrimSuffix(target, "."))
	}
	return ManagedForward{EntryPanelID: line.EntryPanelID, Port: line.Port, Target: target, TargetPort: landing.DesiredPort}, true
}

// TransitForward is the panel's record of one managed line: the row id the
// forward key is minted from, and what the entry server last confirmed.
// The line's own fields stay on the node; this row holds only state the
// sync service owns.
type TransitForward struct {
	ID      int64
	NodeID  int64
	RelayID string
	// Applied is what the entry last confirmed; AppliedAt is nil when it has
	// confirmed nothing (never, or since it rejected the forward).
	Applied   ManagedForward
	AppliedAt *time.Time
	// Traffic is the entry's relayed byte count, carried across counter
	// resets. It is transit traffic and is never charged to a user.
	UpBytes          int64
	DownBytes        int64
	LastRawUp        int64
	LastRawDown      int64
	LastCounterEpoch uint64
}

// ValidateManagedRelays checks the managed lines of one landing node: an entry
// on another server, a dialable address and an explicit port. Entry-server
// existence, its kind and port collisions are checked by the node service,
// which can see other nodes.
func ValidateManagedRelays(landing *Node, lines []RelayLine) error {
	for _, line := range lines {
		if !line.Managed() {
			continue
		}
		if line.EntryPanelID < 0 {
			return fmt.Errorf("%w: invalid relay entry server", ErrValidation)
		}
		if landing != nil && landing.PanelID == line.EntryPanelID {
			return fmt.Errorf("%w: a relay cannot enter through the landing's own server", ErrValidation)
		}
		if strings.TrimSpace(line.Address) == "" {
			return fmt.Errorf("%w: a managed relay needs the address users connect to", ErrValidation)
		}
		if line.Port < 1 || line.Port > 65535 {
			return fmt.Errorf("%w: a managed relay needs an entry port between 1 and 65535", ErrValidation)
		}
		if line.SNI != "" || line.Host != "" {
			return fmt.Errorf("%w: a managed relay forwards unchanged and cannot override SNI or Host", ErrValidation)
		}
	}
	return nil
}
