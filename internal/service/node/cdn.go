package node

import "github.com/KazuhaHub/passwall-sub-panel/internal/domain"

// settleCDN prepares the CDN lines of a save (see domain.SettleCDNLines) and
// checks them against the landing as the save will leave it. previous are the
// lines stored before (nil for a node being made); landing carries the
// transport the lines will sit on. It fills lines in place.
func settleCDN(previous []domain.RelayLine, landing *domain.Node, lines []domain.RelayLine) error {
	if err := domain.SettleCDNLines(previous, lines); err != nil {
		return err
	}
	return domain.ValidateCDNRelays(landing, lines)
}
