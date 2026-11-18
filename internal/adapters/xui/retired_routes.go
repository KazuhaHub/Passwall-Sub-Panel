package xui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

var errInvalidRetiredRouteTemplate = errors.New("invalid 3X-UI routing template")

// retiredRouteCleanup preserves every unrelated JSON value and the ordering
// of the retained rules. Only rules shaped like the retired PSP generator's
// output are eligible, even if another rule uses a similar tag.
func retiredRouteCleanup(config json.RawMessage) (json.RawMessage, []string, error) {
	var root map[string]json.RawMessage
	if json.Unmarshal(config, &root) != nil || root == nil {
		return nil, nil, errInvalidRetiredRouteTemplate
	}

	var routing map[string]json.RawMessage
	if raw := root["routing"]; len(raw) > 0 && json.Unmarshal(raw, &routing) != nil {
		return nil, nil, errInvalidRetiredRouteTemplate
	}
	var rules []json.RawMessage
	if raw := routing["rules"]; len(raw) > 0 && json.Unmarshal(raw, &rules) != nil {
		return nil, nil, errInvalidRetiredRouteTemplate
	}

	kept := make([]json.RawMessage, 0, len(rules))
	var removed []string
	for _, raw := range rules {
		if tag := retiredRuleTag(raw); tag != "" {
			removed = append(removed, tag)
		} else {
			kept = append(kept, raw)
		}
	}
	if len(removed) == 0 {
		return config, nil, nil
	}
	var err error
	routing["rules"], err = json.Marshal(kept)
	if err != nil {
		return nil, nil, err
	}
	root["routing"], err = json.Marshal(routing)
	if err != nil {
		return nil, nil, err
	}
	out, err := json.Marshal(root)
	return out, removed, err
}

func retiredRuleTag(raw json.RawMessage) string {
	var rule struct {
		Type     string   `json:"type"`
		Tag      string   `json:"ruleTag"`
		Inbound  []string `json:"inboundTag"`
		Route    string   `json:"vlessRoute"`
		Outbound string   `json:"outboundTag"`
	}
	if json.Unmarshal(raw, &rule) != nil || rule.Type != "field" ||
		len(rule.Inbound) != 1 || rule.Inbound[0] == "" || rule.Outbound == "" {
		return ""
	}
	identity, ok := strings.CutPrefix(rule.Tag, "psp-vlessroute:")
	if !ok {
		return ""
	}
	node, line, ok := strings.Cut(identity, ":")
	nodeID, nodeErr := strconv.ParseInt(node, 10, 64)
	lineID, lineErr := uuid.Parse(line)
	routeID, routeErr := strconv.ParseUint(rule.Route, 10, 16)
	if !ok || nodeErr != nil || nodeID <= 0 || strconv.FormatInt(nodeID, 10) != node ||
		lineErr != nil || lineID.String() != line || routeErr != nil || routeID == 0 ||
		strconv.FormatUint(routeID, 10) != rule.Route {
		return ""
	}
	return rule.Tag
}

// retiredRouteRevision binds an administrator's confirmation to the remote
// snapshot they reviewed, including the outbound test URL sent on update.
func retiredRouteRevision(config json.RawMessage, testURL string) string {
	hash := sha256.New()
	_, _ = hash.Write(config)
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(testURL))
	return hex.EncodeToString(hash.Sum(nil))
}

// RetiredRouteCleanup reads a 3X-UI template and returns the owned legacy rule
// tags. If revision is supplied, it updates only the snapshot that was previewed.
func (c *Client) RetiredRouteCleanup(ctx context.Context, revision string) (ports.RetiredRouteCleanup, error) {
	ctx = withOp(ctx, "RetiredRouteCleanup")
	var obj json.RawMessage
	if err := c.doJSON(ctx, http.MethodPost, "/panel/api/xray/", url.Values{}, &obj); err != nil {
		return ports.RetiredRouteCleanup{}, errors.New("cannot read 3X-UI routing settings")
	}
	config, testURL, err := retiredRouteTemplate(obj)
	if err != nil {
		return ports.RetiredRouteCleanup{}, err
	}
	next, rules, err := retiredRouteCleanup(config)
	if err != nil {
		return ports.RetiredRouteCleanup{}, err
	}
	preview := ports.RetiredRouteCleanup{RuleTags: rules, Revision: retiredRouteRevision(config, testURL)}
	if revision == "" {
		return preview, nil
	}
	if revision != preview.Revision {
		return ports.RetiredRouteCleanup{}, fmt.Errorf("%w: 3X-UI settings changed since the preview", domain.ErrConflict)
	}
	if len(rules) == 0 {
		return preview, nil
	}
	// 3X-UI substitutes a default when this form field is empty. Do not
	// change an unrelated setting while cleaning routing rules.
	if testURL == "" {
		return ports.RetiredRouteCleanup{}, errors.New("3X-UI did not return its outbound test URL; clean the rules in 3X-UI instead")
	}
	form := url.Values{"xraySetting": {string(next)}, "outboundTestUrl": {testURL}}
	if err := c.doJSON(ctx, http.MethodPost, "/panel/api/xray/update", form, nil); err != nil {
		return ports.RetiredRouteCleanup{}, errors.New("3X-UI routing update was not confirmed; preview again before retrying")
	}
	return preview, nil
}

func retiredRouteTemplate(obj json.RawMessage) (config json.RawMessage, testURL string, err error) {
	var text string
	if json.Unmarshal(obj, &text) == nil {
		obj = json.RawMessage(text)
	}
	var wrapper struct {
		Config  json.RawMessage `json:"xraySetting"`
		TestURL string          `json:"outboundTestUrl"`
	}
	if json.Unmarshal(obj, &wrapper) != nil || len(wrapper.Config) == 0 {
		return nil, "", errInvalidRetiredRouteTemplate
	}
	if json.Unmarshal(wrapper.Config, &text) == nil {
		wrapper.Config = json.RawMessage(text)
	}
	return wrapper.Config, wrapper.TestURL, nil
}
