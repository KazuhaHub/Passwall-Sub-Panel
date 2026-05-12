// Package xui implements ports.XUIClient by talking to a 3X-UI panel's HTTP API.
package xui

import "encoding/json"

// rawInbound mirrors one item of the JSON array returned by
// /panel/api/inbounds/list. Field names follow the 3X-UI backend exactly.
type rawInbound struct {
	ID             int               `json:"id"`
	Up             int64             `json:"up"`
	Down           int64             `json:"down"`
	Total          int64             `json:"total"`
	Remark         string            `json:"remark"`
	Enable         bool              `json:"enable"`
	ExpiryTime     int64             `json:"expiryTime"`
	Listen         string            `json:"listen"`
	Port           int               `json:"port"`
	Protocol       string            `json:"protocol"`
	Settings       string            `json:"settings"`
	StreamSettings string            `json:"streamSettings"`
	Tag            string            `json:"tag"`
	Sniffing       string            `json:"sniffing"`
	Allocate       string            `json:"allocate"`
	ClientStats    []rawClientTraffic `json:"clientStats"`
}

type rawClientTraffic struct {
	ID         int    `json:"id"`
	InboundID  int    `json:"inboundId"`
	Email      string `json:"email"`
	Up         int64  `json:"up"`
	Down       int64  `json:"down"`
	Total      int64  `json:"total"`
	Enable     bool   `json:"enable"`
	ExpiryTime int64  `json:"expiryTime"`
	Reset      int    `json:"reset"`
}

// settingsClients represents the relevant subset of inbound.settings JSON.
// Each entry of Clients carries protocol-specific fields, so we keep them as
// json.RawMessage and let the caller decode them on demand.
type settingsClients struct {
	Clients    []json.RawMessage `json:"clients"`
	Decryption string            `json:"decryption,omitempty"` // VLESS
	Fallbacks  json.RawMessage   `json:"fallbacks,omitempty"`  // VLESS / Trojan
	Method     string            `json:"method,omitempty"`     // SS / SS-2022
	Password   string            `json:"password,omitempty"`   // SS server-side PSK
	Network    string            `json:"network,omitempty"`    // SS
}

// genericResponse is the envelope used by most 3X-UI endpoints.
type genericResponse struct {
	Success bool            `json:"success"`
	Msg     string          `json:"msg"`
	Obj     json.RawMessage `json:"obj"`
}
