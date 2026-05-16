// Package xrayspec contains pure parsers for the Xray-core JSON shapes
// stored by 3X-UI in inbound.settings / inbound.streamSettings. These are
// I/O-free helpers so any layer can use them without importing an adapter.
package xrayspec

import (
	"bytes"
	"encoding/json"
	"strconv"
)

// FlexString tolerates JSON values that come in as either a string or a
// number, normalising to string. 3X-UI's admin UI lets operators type a
// numeric Telegram ID directly into tgId, which the server then stores as a
// raw JSON number — but other rows for the same inbound (and the panel's
// own writes) use the quoted form. A plain `string` field rejected the
// numeric form and broke every reader of inbound.settings for that inbound.
type FlexString string

func (f *FlexString) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		*f = ""
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = FlexString(s)
		return nil
	}
	// Number — keep the raw token so int64 IDs don't lose precision via
	// float64 round-tripping.
	if _, err := strconv.ParseFloat(string(b), 64); err != nil {
		return err
	}
	*f = FlexString(string(b))
	return nil
}

func (f FlexString) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(f))
}

// InboundClient is one entry of inbound.settings.clients[], normalised
// across protocols. Fields not present in the source JSON come back zero
// or nil. Use IsEnabled() to read the effective enable flag — 3X-UI omits
// the field when it would equal Go's zero value, so a missing field means
// "enabled" (Xray default), not "disabled".
type InboundClient struct {
	ID         string     `json:"id,omitempty"`
	Email      string     `json:"email,omitempty"`
	Enable     *bool      `json:"enable,omitempty"`
	Flow       string     `json:"flow,omitempty"`
	Password   string     `json:"password,omitempty"`
	LimitIP    int        `json:"limitIp,omitempty"`
	TotalGB    int64      `json:"totalGB,omitempty"`
	ExpiryTime int64      `json:"expiryTime,omitempty"`
	SubID      string     `json:"subId,omitempty"`
	TgID       FlexString `json:"tgId,omitempty"`
	Reset      int        `json:"reset,omitempty"`
}

// IsEnabled returns the effective enable flag. Missing field is treated
// as true to match Xray's default behaviour.
func (c InboundClient) IsEnabled() bool {
	return c.Enable == nil || *c.Enable
}

// InboundSettings is the union of fields the panel cares about from
// inbound.settings JSON across all protocols.
type InboundSettings struct {
	// SS / SS-2022
	Method   string `json:"method,omitempty"`
	Password string `json:"password,omitempty"` // SS-2022 server PSK
	Network  string `json:"network,omitempty"`  // optional SS network field

	Clients []InboundClient `json:"clients,omitempty"`
}

// ParseSettings decodes inbound.settings JSON; an empty string is treated
// as an empty (zero-value) settings struct.
func ParseSettings(settingsJSON string) (*InboundSettings, error) {
	out := &InboundSettings{}
	if settingsJSON == "" {
		return out, nil
	}
	if err := json.Unmarshal([]byte(settingsJSON), out); err != nil {
		return nil, err
	}
	return out, nil
}

// FindClient returns a pointer to the client with the given email, or nil.
func FindClient(clients []InboundClient, email string) *InboundClient {
	for i := range clients {
		if clients[i].Email == email {
			return &clients[i]
		}
	}
	return nil
}
