package vlessroute

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// reserveExistingRoutes avoids assigning new values already handled by a
// user/peer rule in this inbound. Existing line IDs never get silently remapped.
func reserveExistingRoutes(template ports.RouteTemplate, nodeID int64, inboundTag string, registered []domain.VlessRouteLine, used map[uint16]bool) error {
	var root struct {
		Routing struct {
			Rules []struct {
				Tag         string          `json:"ruleTag"`
				InboundTags []string        `json:"inboundTag"`
				Route       json.RawMessage `json:"vlessRoute"`
			} `json:"rules"`
		} `json:"routing"`
	}
	if json.Unmarshal(template.Config, &root) != nil {
		return fmt.Errorf("%w: cannot inspect existing route values", domain.ErrValidation)
	}
	owned := map[string]bool{}
	for _, line := range registered {
		owned[RuleTag(nodeID, line.ID)] = true
	}
	for _, rule := range root.Routing.Rules {
		if owned[rule.Tag] || len(rule.Route) == 0 {
			continue
		}
		applies := len(rule.InboundTags) == 0
		for _, tag := range rule.InboundTags {
			applies = applies || tag == inboundTag
		}
		if !applies {
			continue
		}
		var spec string
		if json.Unmarshal(rule.Route, &spec) != nil {
			var value int
			if json.Unmarshal(rule.Route, &value) != nil {
				return fmt.Errorf("%w: unsupported existing route matcher", domain.ErrValidation)
			}
			spec = strconv.Itoa(value)
		}
		for _, part := range strings.Split(spec, ",") {
			rangeParts := strings.Split(strings.TrimSpace(part), "-")
			if len(rangeParts) < 1 || len(rangeParts) > 2 {
				return fmt.Errorf("%w: invalid existing route range", domain.ErrValidation)
			}
			first, err := strconv.Atoi(strings.TrimSpace(rangeParts[0]))
			if err != nil || first < 0 || first > 65535 {
				return fmt.Errorf("%w: invalid existing route range", domain.ErrValidation)
			}
			last := first
			if len(rangeParts) == 2 {
				last, err = strconv.Atoi(strings.TrimSpace(rangeParts[1]))
				if err != nil || last < first || last > 65535 {
					return fmt.Errorf("%w: invalid existing route range", domain.ErrValidation)
				}
			}
			for route := first; route <= last; route++ {
				used[uint16(route)] = true
			}
		}
	}
	return nil
}
