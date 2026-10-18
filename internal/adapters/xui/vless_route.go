package xui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

var _ ports.VlessRouteAdapter = (*Client)(nil)

func (c *Client) ReadRouteTemplate(ctx context.Context) (ports.RouteTemplate, error) {
	ctx = withOp(ctx, "ReadRouteTemplate")
	var obj json.RawMessage
	if err := c.doJSON(ctx, http.MethodPost, "/panel/api/xray/", url.Values{}, &obj); err != nil {
		return ports.RouteTemplate{}, errors.New("cannot read 3X-UI routing settings")
	}
	return decodeRouteTemplate(obj)
}

func decodeRouteTemplate(obj json.RawMessage) (ports.RouteTemplate, error) {
	var encoded string
	if json.Unmarshal(obj, &encoded) == nil {
		obj = json.RawMessage(encoded)
	}
	var wrapper struct {
		Config       json.RawMessage `json:"xraySetting"`
		TestURL      string          `json:"outboundTestUrl"`
		Subscription []struct {
			Tag      string `json:"tag"`
			Protocol string `json:"protocol"`
		} `json:"subscriptionOutbounds"`
		SubscriptionTags []string `json:"subscriptionOutboundTags"`
	}
	invalid := errors.New("invalid 3X-UI routing settings")
	if json.Unmarshal(obj, &wrapper) != nil || len(wrapper.Config) == 0 {
		return ports.RouteTemplate{}, invalid
	}
	if json.Unmarshal(wrapper.Config, &encoded) == nil {
		wrapper.Config = json.RawMessage(encoded)
	}
	var template struct {
		Outbounds []struct {
			Tag      string `json:"tag"`
			Protocol string `json:"protocol"`
		} `json:"outbounds"`
	}
	if json.Unmarshal(wrapper.Config, &template) != nil || string(wrapper.Config) == "null" {
		return ports.RouteTemplate{}, invalid
	}
	result := ports.RouteTemplate{Config: wrapper.Config, TestURL: wrapper.TestURL, Outbounds: []ports.RouteOutbound{}}
	seen := map[string]bool{}
	add := func(tag, protocol string) error {
		if strings.TrimSpace(tag) == "" {
			return nil
		}
		if seen[tag] {
			return errors.New("duplicate 3X-UI outbound tags")
		}
		seen[tag] = true
		result.Outbounds = append(result.Outbounds, ports.RouteOutbound{Tag: tag, Protocol: protocol})
		return nil
	}
	for _, o := range template.Outbounds {
		if err := add(o.Tag, o.Protocol); err != nil {
			return ports.RouteTemplate{}, err
		}
	}
	for _, o := range wrapper.Subscription {
		if err := add(o.Tag, o.Protocol); err != nil {
			return ports.RouteTemplate{}, err
		}
	}
	for _, tag := range wrapper.SubscriptionTags {
		if !seen[tag] {
			if err := add(tag, ""); err != nil {
				return ports.RouteTemplate{}, err
			}
		}
	}
	sort.Slice(result.Outbounds, func(i, j int) bool { return result.Outbounds[i].Tag < result.Outbounds[j].Tag })
	return result, nil
}

func (c *Client) WriteRouteTemplate(ctx context.Context, template ports.RouteTemplate) error {
	ctx = withOp(ctx, "WriteRouteTemplate")
	// Never retry an ambiguous remote write: read the recorded target first.
	if err := c.doJSON(ctx, http.MethodPost, "/panel/api/xray/update", url.Values{"xraySetting": {string(template.Config)}, "outboundTestUrl": {template.TestURL}}, nil); err != nil {
		return errors.New("3X-UI routing update was not confirmed")
	}
	return nil
}
