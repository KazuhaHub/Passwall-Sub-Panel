// Package xui implements ports.XUIClient by talking to a 3X-UI panel's HTTP API.
package xui

import "encoding/json"

// rawInbound mirrors one item of the JSON array returned by
// /panel/api/inbounds/list. Field names follow the 3X-UI backend exactly.
//
// settings/streamSettings/sniffing/allocate use flexJSON because 3X-UI 3.1.0
// returns them as nested JSON objects while ≤ 3.0.x returns escaped strings.
// flexJSON normalises both into the canonical unescaped JSON text that
// downstream parsers (xrayspec.ParseSettings, extractSSMethod, ...) expect.
type rawInbound struct {
	ID             int                `json:"id"`
	Up             int64              `json:"up"`
	Down           int64              `json:"down"`
	Total          int64              `json:"total"`
	Remark         string             `json:"remark"`
	Enable         bool               `json:"enable"`
	ExpiryTime     int64              `json:"expiryTime"`
	Listen         string             `json:"listen"`
	Port           int                `json:"port"`
	Protocol       string             `json:"protocol"`
	Settings       flexJSON           `json:"settings"`
	StreamSettings flexJSON           `json:"streamSettings"`
	Tag            string             `json:"tag"`
	Sniffing       flexJSON           `json:"sniffing"`
	Allocate       flexJSON           `json:"allocate"`
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

// genericResponse is the envelope used by most 3X-UI endpoints.
type genericResponse struct {
	Success bool            `json:"success"`
	Msg     string          `json:"msg"`
	Obj     json.RawMessage `json:"obj"`
}
