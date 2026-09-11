// Package clientdoc owns the one canonical construction of PSP's desired
// client state. Panel adapters and the future native protocol adapter consume
// this value; they do not re-read User or live panel state to rebuild intent.
package clientdoc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type Attachment struct {
	NodeID       int64  `json:"node_id"`
	FlowOverride string `json:"flow_override"`
}

// Document is PSP's backend-neutral desired client. Compatibility-only
// properties (Email, connection caps) live here beside native roster inputs;
// an adapter explicitly projects the subset its backend owns.
type Document struct {
	ClientID int64 `json:"client_id"`
	UserID   int64 `json:"user_id"`
	PanelID  int64 `json:"panel_id"`

	Email      string `json:"email"`
	UUID       string `json:"uuid"`
	Password   string `json:"password"`
	Enable     bool   `json:"enable"`
	ExpiryTime int64  `json:"expiry_time"`
	// Compatibility-only lifecycle fields are deliberately OUTSIDE canonical
	// client content. Quota and IP policy belong to the independently-versioned
	// directives stream; device limit has no native data-plane executor. Keeping
	// json:"-" here prevents traffic observations from churning the roster ETag.
	IPLimit       int   `json:"-"`
	DeviceLimit   int   `json:"-"`
	QuotaHeadroom int64 `json:"-"`

	Attachments []Attachment `json:"attachments"`
}

// Mint is the sole constructor for desired client documents. It reads only
// PSP-owned persisted intent and sorts attachments by stable row identity, so
// identical logical input has byte-identical canonical JSON regardless of
// query order.
func Mint(client *domain.PSPClient, inbounds []domain.PSPClientInbound) Document {
	if client == nil {
		return Document{Attachments: []Attachment{}}
	}
	want := client.DesiredLifecycle()
	doc := Document{
		ClientID: client.ID, UserID: client.UserID, PanelID: client.PanelID,
		Email: client.Email, UUID: client.UUID, Password: client.Password,
		Enable: want.Enable, ExpiryTime: want.ExpiryTime,
		IPLimit: want.IPLimit, DeviceLimit: want.DeviceLimit,
		QuotaHeadroom: want.QuotaHeadroom,
		Attachments:   make([]Attachment, len(inbounds)),
	}
	for i, inbound := range inbounds {
		doc.Attachments[i] = Attachment{NodeID: inbound.NodeID, FlowOverride: inbound.FlowOverride}
	}
	sort.Slice(doc.Attachments, func(i, j int) bool {
		if doc.Attachments[i].NodeID != doc.Attachments[j].NodeID {
			return doc.Attachments[i].NodeID < doc.Attachments[j].NodeID
		}
		return doc.Attachments[i].FlowOverride < doc.Attachments[j].FlowOverride
	})
	return doc
}

// Canonical returns the stable JSON and its pure-content SHA-256 ETag. No
// version or timestamp can enter this serialization.
func (d Document) Canonical() ([]byte, string, error) {
	body, err := json.Marshal(d)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(body)
	return body, hex.EncodeToString(sum[:]), nil
}

// Lifecycle exposes the backend-neutral lifecycle projection without making
// adapters reach back into User or the mutable database row.
func (d Document) Lifecycle() domain.UserLifecycle {
	return domain.UserLifecycle{
		Enable: d.Enable, ExpiryTime: d.ExpiryTime, QuotaHeadroom: d.QuotaHeadroom,
		IPLimit: d.IPLimit, DeviceLimit: d.DeviceLimit,
	}
}
