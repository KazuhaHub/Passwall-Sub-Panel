package render

import (
	"regexp"
	"strconv"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// factorPlaceholderRE matches the opt-in "{{ factor }}" token in a node, relay
// line or route line name, spaced like every other PSP placeholder.
var factorPlaceholderRE = regexp.MustCompile(`\{\{\s*factor\s*\}\}`)

// formatTrafficFactor renders a factor in thousandths as a multiplier label:
// 1500 → "×1.5", 1000 → "×1", 0 → "×0".
func formatTrafficFactor(milli int) string {
	return "×" + strconv.FormatFloat(float64(milli)/1000, 'f', -1, 64)
}

// applyFactorPlaceholder substitutes "{{ factor }}" in each entry's name with
// the traffic factor of the server the user's traffic is counted on: the
// node's own server, for its relay and route lines as well (a relay only
// forwards to it). Names without the token are untouched, so a subscription
// changes only where an administrator asked for the label.
func applyFactorPlaceholder(items []renderItem, factorOf func(panelID int64) int) {
	for i := range items {
		it := &items[i]
		if it.node == nil || it.isSeparator || !factorPlaceholderRE.MatchString(it.name) {
			continue
		}
		it.name = ReplaceFactorPlaceholder(it.name, factorOf(it.node.PanelID))
	}
}

// ReplaceFactorPlaceholder substitutes "{{ factor }}" in one name, for the
// user portal's server list, which shows the same names as the subscription.
func ReplaceFactorPlaceholder(name string, milli int) string {
	if !factorPlaceholderRE.MatchString(name) {
		return name
	}
	return factorPlaceholderRE.ReplaceAllLiteralString(name, formatTrafficFactor(milli))
}

// TrafficFactorOf is a server's traffic factor through the pool.
func (s *Service) TrafficFactorOf(panelID int64) int {
	if s == nil {
		return domain.TrafficFactorDefault
	}
	return s.trafficFactorOf(panelID)
}

func (s *Service) trafficFactorOf(panelID int64) int {
	if s.pool == nil {
		return domain.TrafficFactorDefault
	}
	return ports.PanelTrafficFactor(s.pool, panelID)
}
