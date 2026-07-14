package sui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type inboundSummary struct {
	ID         int      `json:"id"`
	Type       string   `json:"type"`
	Tag        string   `json:"tag"`
	TLSID      int      `json:"tls_id"`
	Listen     string   `json:"listen"`
	ListenPort int      `json:"listen_port"`
	Users      []string `json:"users"`
}

type tlsModel struct {
	ID     int             `json:"id,omitempty"`
	Name   string          `json:"name"`
	Server json.RawMessage `json:"server"`
	Client json.RawMessage `json:"client"`
}

func (c *Client) inboundSummaries(ctx context.Context) ([]inboundSummary, error) {
	var obj struct {
		Inbounds []inboundSummary `json:"inbounds"`
	}
	if err := c.do(ctx, http.MethodGet, "inbounds", nil, &obj); err != nil {
		return nil, err
	}
	return obj.Inbounds, nil
}

func (c *Client) tlsModels(ctx context.Context) (map[int]tlsModel, error) {
	var obj struct {
		TLS []tlsModel `json:"tls"`
	}
	if err := c.do(ctx, http.MethodGet, "tls", nil, &obj); err != nil {
		return nil, err
	}
	out := make(map[int]tlsModel, len(obj.TLS))
	for _, item := range obj.TLS {
		out[item.ID] = item
	}
	return out, nil
}

func (c *Client) fullInbound(ctx context.Context, id int) (map[string]any, error) {
	var obj struct {
		Inbounds []map[string]any `json:"inbounds"`
	}
	if err := c.do(ctx, http.MethodGet, "inbounds?id="+strconv.Itoa(id), nil, &obj); err != nil {
		return nil, err
	}
	if len(obj.Inbounds) == 0 {
		return nil, nil
	}
	return obj.Inbounds[0], nil
}

func (c *Client) ListInbounds(ctx context.Context) ([]ports.Inbound, error) {
	summaries, err := c.inboundSummaries(ctx)
	if err != nil {
		return nil, err
	}
	tlsByID, err := c.tlsModels(ctx)
	if err != nil {
		return nil, err
	}
	clients, err := c.listClients(ctx)
	if err != nil {
		return nil, err
	}
	byInbound := make(map[int][]ports.ClientTraffic)
	for _, client := range clients {
		for _, id := range client.Inbounds {
			byInbound[id] = append(byInbound[id], ports.ClientTraffic{
				ID: client.ID, InboundID: id, Email: client.Name,
				Up: client.Up + client.TotalUp, Down: client.Down + client.TotalDown,
				Total:  client.Up + client.Down + client.TotalUp + client.TotalDown,
				Enable: client.Enable, ExpiryTime: client.Expiry * 1000,
				LastOnline: client.OnlineAt * 1000,
			})
		}
	}
	out := make([]ports.Inbound, 0, len(summaries))
	for _, summary := range summaries {
		full, err := c.fullInbound(ctx, summary.ID)
		if err != nil {
			return nil, err
		}
		if full == nil {
			continue
		}
		inbound, err := normaliseInbound(summary, full, tlsByID)
		if err != nil {
			return nil, fmt.Errorf("S-UI inbound %d: %w", summary.ID, err)
		}
		inbound.ClientStats = byInbound[summary.ID]
		out = append(out, inbound)
	}
	return out, nil
}

func (c *Client) ListInboundsSlim(ctx context.Context) ([]ports.Inbound, error) {
	return c.ListInbounds(ctx)
}

func (c *Client) GetInbound(ctx context.Context, id int) (*ports.Inbound, error) {
	items, err := c.ListInbounds(ctx)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].ID == id {
			return &items[i], nil
		}
	}
	return nil, fmt.Errorf("%w: S-UI inbound %d not found", domain.ErrNotFound, id)
}

func normaliseInbound(summary inboundSummary, raw map[string]any, tlsByID map[int]tlsModel) (ports.Inbound, error) {
	settings := map[string]any{"clients": []any{}}
	if method, ok := raw["method"].(string); ok {
		settings["method"] = method
	}
	if password, ok := raw["password"].(string); ok {
		settings["password"] = password
	}
	if network, ok := raw["network"].(string); ok {
		settings["network"] = network
	}
	settingsJSON, _ := json.Marshal(settings)

	stream := map[string]any{"network": "tcp", "security": "none"}
	sockopt := map[string]any{}
	if mark := intValue(raw["routing_mark"]); mark > 0 {
		sockopt["mark"] = mark
	}
	if boolValue(raw["tcp_fast_open"]) {
		sockopt["tcpFastOpen"] = true
	}
	if seconds := durationSeconds(raw["tcp_keep_alive_interval"]); seconds > 0 {
		sockopt["tcpKeepAliveInterval"] = seconds
	}
	if seconds := durationSeconds(raw["tcp_keep_alive"]); seconds > 0 {
		sockopt["tcpKeepAliveIdle"] = seconds
	}
	if len(sockopt) > 0 {
		stream["sockopt"] = sockopt
	}
	if transport, ok := raw["transport"].(map[string]any); ok {
		switch kind, _ := transport["type"].(string); kind {
		case "ws":
			stream["network"] = "ws"
			stream["wsSettings"] = map[string]any{
				"path": transport["path"], "host": transport["host"], "headers": transport["headers"],
			}
		case "grpc":
			stream["network"] = "grpc"
			stream["grpcSettings"] = map[string]any{"serviceName": transport["service_name"]}
		case "httpupgrade":
			stream["network"] = "httpupgrade"
			stream["httpupgradeSettings"] = map[string]any{
				"path": transport["path"], "host": transport["host"], "headers": transport["headers"],
			}
		case "http":
			stream["network"] = "http"
			stream["httpSettings"] = map[string]any{"path": transport["path"], "host": transport["host"]}
		}
	}
	tlsID := intValue(raw["tls_id"])
	if tlsID > 0 && len(tlsByID[tlsID].Server) > 0 {
		model := tlsByID[tlsID]
		var tlsServer, tlsClient map[string]any
		if err := json.Unmarshal(model.Server, &tlsServer); err != nil {
			return ports.Inbound{}, err
		}
		if len(model.Client) > 0 {
			if err := json.Unmarshal(model.Client, &tlsClient); err != nil {
				return ports.Inbound{}, err
			}
		}
		if tlsClient == nil {
			tlsClient = map[string]any{}
		}
		if reality, ok := tlsServer["reality"].(map[string]any); ok && boolValue(reality["enabled"]) {
			stream["security"] = "reality"
			handshake, _ := reality["handshake"].(map[string]any)
			dest := stringValue(handshake["server"])
			if port := intValue(handshake["server_port"]); port > 0 {
				dest += ":" + strconv.Itoa(port)
			}
			clientReality, _ := tlsClient["reality"].(map[string]any)
			serverNames := stringSlice(tlsClient["server_name"])
			if len(serverNames) == 0 {
				serverNames = stringSlice(tlsServer["server_name"])
			}
			fingerprint := stringValue(mapValue(tlsClient["utls"])["fingerprint"])
			if fingerprint == "" {
				fingerprint = "chrome"
			}
			stream["realitySettings"] = map[string]any{
				"dest": dest, "serverNames": serverNames,
				"privateKey": reality["private_key"], "shortIds": reality["short_id"],
				"maxTimediff": durationMillis(reality["max_time_difference"]),
				"settings": map[string]any{
					"fingerprint": fingerprint, "publicKey": clientReality["public_key"],
				},
			}
		} else if boolValue(tlsServer["enabled"]) {
			stream["security"] = "tls"
			serverName := firstString(tlsClient["server_name"])
			if serverName == "" {
				serverName = firstString(tlsServer["server_name"])
			}
			alpn := tlsClient["alpn"]
			if alpn == nil {
				alpn = tlsServer["alpn"]
			}
			tlsSettings := map[string]any{
				"serverName": serverName, "alpn": alpn,
				"minVersion": tlsServer["min_version"], "maxVersion": tlsServer["max_version"],
				"allowInsecure": boolValue(tlsClient["insecure"]),
			}
			if fingerprint := stringValue(mapValue(tlsClient["utls"])["fingerprint"]); fingerprint != "" {
				tlsSettings["settings"] = map[string]any{"fingerprint": fingerprint}
			}
			cert := map[string]any{
				"certificateFile": tlsServer["certificate_path"], "keyFile": tlsServer["key_path"],
				"certificate": tlsServer["certificate"], "key": tlsServer["key"],
			}
			if hasNonEmptyValue(cert) {
				tlsSettings["certificates"] = []any{cert}
			}
			stream["tlsSettings"] = tlsSettings
		}
	}
	if summary.Type == "hysteria2" {
		stream["network"] = "hysteria"
		if obfs, ok := raw["obfs"].(map[string]any); ok && stringValue(obfs["type"]) == "salamander" {
			stream["finalmask"] = map[string]any{"udp": []any{map[string]any{
				"type": "salamander", "settings": map[string]any{"password": obfs["password"]},
			}}}
		}
		hysteriaSettings := map[string]any{"protocol": "hysteria2", "version": 2}
		if timeout := durationSeconds(raw["udp_timeout"]); timeout > 0 {
			hysteriaSettings["udpIdleTimeout"] = timeout
		}
		if masquerade, ok := raw["masquerade"].(map[string]any); ok {
			legacy := map[string]any{"type": masquerade["type"]}
			switch stringValue(masquerade["type"]) {
			case "proxy":
				legacy["url"] = masquerade["url"]
			case "file":
				legacy["dir"] = masquerade["directory"]
			case "string":
				legacy["content"] = masquerade["content"]
			}
			hysteriaSettings["masquerade"] = legacy
		}
		stream["hysteriaSettings"] = hysteriaSettings
	}
	streamJSON, _ := json.Marshal(stream)
	return ports.Inbound{
		ID: summary.ID, Remark: summary.Tag, Enable: true,
		Listen:   coalesce(summary.Listen, stringValue(raw["listen"])),
		Port:     firstPositive(summary.ListenPort, intValue(raw["listen_port"])),
		Protocol: summary.Type, Settings: string(settingsJSON), StreamSettings: string(streamJSON), Tag: summary.Tag,
	}, nil
}

func intValue(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	}
	return 0
}
func boolValue(v any) bool     { b, _ := v.(bool); return b }
func stringValue(v any) string { s, _ := v.(string); return s }
func mapValue(v any) map[string]any {
	m, _ := v.(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return m
}
func hasNonEmptyValue(values map[string]any) bool {
	for _, value := range values {
		switch typed := value.(type) {
		case string:
			if strings.TrimSpace(typed) != "" {
				return true
			}
		case nil:
		default:
			return true
		}
	}
	return false
}
func stringSlice(v any) []string {
	if s := stringValue(v); s != "" {
		return []string{s}
	}
	if values, ok := v.([]any); ok {
		out := make([]string, 0, len(values))
		for _, item := range values {
			if s := stringValue(item); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
func firstString(v any) string {
	values := stringSlice(v)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
func coalesce(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func durationSeconds(v any) int {
	switch value := v.(type) {
	case string:
		d, err := time.ParseDuration(value)
		if err == nil && d > 0 {
			return int(d / time.Second)
		}
	case float64:
		return int(value)
	case int:
		return value
	}
	return 0
}

func durationMillis(v any) int64 {
	switch value := v.(type) {
	case string:
		d, err := time.ParseDuration(value)
		if err == nil && d > 0 {
			return d.Milliseconds()
		}
	case float64:
		return int64(value)
	case int64:
		return value
	case int:
		return int64(value)
	}
	return 0
}
