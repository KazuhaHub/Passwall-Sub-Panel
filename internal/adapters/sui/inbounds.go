package sui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type inboundSummary struct {
	ID         int      `json:"id"`
	Type       string   `json:"type"`
	Tag        string   `json:"tag"`
	Listen     string   `json:"listen"`
	ListenPort int      `json:"listen_port"`
	Users      []string `json:"users"`
}

type tlsModel struct {
	ID     int             `json:"id"`
	Server json.RawMessage `json:"server"`
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

func (c *Client) tlsModels(ctx context.Context) (map[int]json.RawMessage, error) {
	var obj struct {
		TLS []tlsModel `json:"tls"`
	}
	if err := c.do(ctx, http.MethodGet, "tls", nil, &obj); err != nil {
		return nil, err
	}
	out := make(map[int]json.RawMessage, len(obj.TLS))
	for _, item := range obj.TLS {
		out[item.ID] = item.Server
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

func normaliseInbound(summary inboundSummary, raw map[string]any, tlsByID map[int]json.RawMessage) (ports.Inbound, error) {
	settings := map[string]any{"clients": []any{}}
	if method, ok := raw["method"].(string); ok {
		settings["method"] = method
	}
	if password, ok := raw["password"].(string); ok {
		settings["password"] = password
	}
	settingsJSON, _ := json.Marshal(settings)

	stream := map[string]any{"network": "tcp", "security": "none"}
	if transport, ok := raw["transport"].(map[string]any); ok {
		switch kind, _ := transport["type"].(string); kind {
		case "ws":
			stream["network"] = "ws"
			stream["wsSettings"] = map[string]any{"path": transport["path"], "headers": transport["headers"]}
		case "grpc":
			stream["network"] = "grpc"
			stream["grpcSettings"] = map[string]any{"serviceName": transport["service_name"]}
		case "httpupgrade":
			stream["network"] = "httpupgrade"
		case "http":
			stream["network"] = "http"
		}
	}
	tlsID := intValue(raw["tls_id"])
	if tlsID > 0 && len(tlsByID[tlsID]) > 0 {
		var tls map[string]any
		if err := json.Unmarshal(tlsByID[tlsID], &tls); err != nil {
			return ports.Inbound{}, err
		}
		if reality, ok := tls["reality"].(map[string]any); ok && boolValue(reality["enabled"]) {
			stream["security"] = "reality"
			handshake, _ := reality["handshake"].(map[string]any)
			dest := stringValue(handshake["server"])
			if port := intValue(handshake["server_port"]); port > 0 {
				dest += ":" + strconv.Itoa(port)
			}
			stream["realitySettings"] = map[string]any{
				"dest": dest, "serverNames": stringSlice(tls["server_name"]),
				"privateKey": reality["private_key"], "shortIds": reality["short_id"],
				"settings": map[string]any{"fingerprint": "chrome"},
			}
		} else if boolValue(tls["enabled"]) {
			stream["security"] = "tls"
			stream["tlsSettings"] = map[string]any{
				"serverName": firstString(tls["server_name"]), "alpn": tls["alpn"],
			}
		}
	}
	if summary.Type == "hysteria2" {
		stream["network"] = "hysteria"
		if obfs, ok := raw["obfs"].(map[string]any); ok && stringValue(obfs["type"]) == "salamander" {
			stream["finalmask"] = map[string]any{"udp": []any{map[string]any{
				"type": "salamander", "settings": map[string]any{"password": obfs["password"]},
			}}}
		}
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
