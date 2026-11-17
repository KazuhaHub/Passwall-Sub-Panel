package render

import (
	"regexp"
	"strconv"
)

// factorPlaceholderRE matches the opt-in "{{ factor }}" token in a node or relay
// line name, spaced like every other PSP placeholder.
var factorPlaceholderRE = regexp.MustCompile(`\{\{\s*factor\s*\}\}`)

// formatTrafficFactor renders a factor in thousandths as a multiplier label:
// 1500 → "×1.5", 1000 → "×1", 0 → "×0".
func formatTrafficFactor(milli int) string {
	return "×" + strconv.FormatFloat(float64(milli)/1000, 'f', -1, 64)
}

// applyFactorPlaceholder substitutes "{{ factor }}" in each entry's name with
// the factor of that entry's own line: a relay entry's relay line, the direct
// entry's node. Names without the token are untouched, so a subscription
// changes only where an administrator asked for the label.
func applyFactorPlaceholder(items []renderItem) {
	for i := range items {
		it := &items[i]
		if it.node == nil || it.isSeparator || !factorPlaceholderRE.MatchString(it.name) {
			continue
		}
		it.name = ReplaceFactorPlaceholder(it.name, it.lineFactor())
	}
}

// lineFactor is the traffic factor of the line this entry renders.
func (it renderItem) lineFactor() int {
	if it.relay != nil {
		return it.relay.EffectiveTrafficFactor()
	}
	return it.node.EffectiveTrafficFactor()
}

// ReplaceFactorPlaceholder substitutes "{{ factor }}" in one name, for the
// user portal's server list, which shows the same names as the subscription.
func ReplaceFactorPlaceholder(name string, milli int) string {
	if !factorPlaceholderRE.MatchString(name) {
		return name
	}
	return factorPlaceholderRE.ReplaceAllLiteralString(name, formatTrafficFactor(milli))
}
