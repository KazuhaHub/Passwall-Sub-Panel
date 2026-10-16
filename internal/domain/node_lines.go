package domain

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// DirectLineID is scoped by the parent NodeID, just like relay IDs.
const DirectLineID = "direct"

// AssignRelayIDs fills missing IDs at creation/migration, never during reads.
func AssignRelayIDs(lines []RelayLine) bool {
	changed := false
	for i := range lines {
		if lines[i].ID == "" {
			lines[i].ID = uuid.NewString()
			changed = true
		}
	}
	return changed
}

// ValidateRelayIDs reserves the original-line identity and prevents a single
// precise reference from accidentally selecting two entries on one node.
func ValidateRelayIDs(lines []RelayLine) error {
	seen := map[string]bool{}
	for _, line := range lines {
		if line.ID == "" || line.ID == DirectLineID || strings.TrimSpace(line.ID) != line.ID || len(line.ID) > 128 || seen[line.ID] {
			return fmt.Errorf("%w: invalid or duplicate relay line ID", ErrValidation)
		}
		seen[line.ID] = true
	}
	return nil
}

// ReconcileRelayIDs preserves IDs for old API clients that omit them. An
// ambiguous replacement must be refreshed rather than retargeting saved rules.
func ReconcileRelayIDs(previous, next []RelayLine) error {
	known := make(map[string]RelayLine, len(previous))
	for _, line := range previous {
		known[line.ID] = line
	}
	used := map[string]bool{}
	hasExplicit, generated := false, false
	for _, line := range next {
		hasExplicit = hasExplicit || line.ID != ""
	}
	for i := range next {
		line := &next[i]
		if line.ID == "" {
			var matches []string
			var exact []string
			for _, old := range previous {
				if !used[old.ID] && old.Address == line.Address && old.Port == line.Port && old.SNI == line.SNI && old.Host == line.Host {
					matches = append(matches, old.ID)
					if old.Name == line.Name {
						exact = append(exact, old.ID)
					}
				}
			}
			if len(exact) > 0 {
				matches = exact
			}
			if len(matches) > 1 {
				return fmt.Errorf("%w: ambiguous relay identity; reload node detail", ErrValidation)
			}
			if len(matches) == 1 {
				line.ID = matches[0]
			} else {
				line.ID = uuid.NewString()
				generated = true
			}
		} else if _, ok := known[line.ID]; !ok {
			return fmt.Errorf("%w: unknown relay line ID; reload node detail", ErrValidation)
		}
		if line.ID == DirectLineID || used[line.ID] || strings.TrimSpace(line.ID) != line.ID {
			return fmt.Errorf("%w: invalid or duplicate relay line ID", ErrValidation)
		}
		used[line.ID] = true
	}
	if !hasExplicit && generated {
		for _, old := range previous {
			if !used[old.ID] {
				return fmt.Errorf("%w: cannot identify replaced legacy relay; reload node detail", ErrValidation)
			}
		}
	}
	return nil
}

// HasLine includes disabled/hidden entries so references remain identifiable.
func (n *Node) HasLine(id string) bool {
	if n == nil {
		return false
	}
	if id == DirectLineID {
		return true
	}
	for _, relay := range n.Relays {
		if relay.ID == id && id != "" {
			return true
		}
	}
	for _, line := range n.RouteState.Lines {
		if line.ID == id && id != "" && !line.Deleted {
			return true
		}
	}
	return false
}
