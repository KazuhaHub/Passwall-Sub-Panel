package domain

// Staff-visible summaries contain a mode only, never owned-list identities or
// contents. Empty or inconsistent stored values are not an open-mode proof.
type DestGroupAccessMode string

const (
	DestGroupAccessOpen    DestGroupAccessMode = "open"
	DestGroupAccessTrial   DestGroupAccessMode = "allowlist_trial"
	DestGroupAccessEnforce DestGroupAccessMode = "allowlist_enforce"
)

func DestinationGroupAccessMode(mode, stage string) (DestGroupAccessMode, error) {
	if mode == "open" && stage == "" {
		return DestGroupAccessOpen, nil
	}
	if mode == "allowlist" {
		switch stage {
		case "trial":
			return DestGroupAccessTrial, nil
		case "enforce":
			return DestGroupAccessEnforce, nil
		}
	}
	return "", ErrValidation
}
