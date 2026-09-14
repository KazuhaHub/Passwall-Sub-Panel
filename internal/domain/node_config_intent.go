package domain

// NodeConfigIntent is the PSP-owned, client-less listener configuration minted
// for native nodes. Minting and applied-receipt checks share this comparable
// value so a new listener field cannot silently escape the confirmation guard.
// It is an expected-value guard, never a desired-state write payload.
type NodeConfigIntent struct {
	Enabled        bool   `json:"enabled"`
	Listen         string `json:"listen"`
	Port           int    `json:"port"`
	Protocol       string `json:"protocol"`
	Remark         string `json:"remark"`
	Settings       string `json:"settings"`
	StreamSettings string `json:"stream_settings"`
	Sniffing       string `json:"sniffing"`
	Allocate       string `json:"allocate"`
	ExpiryTime     int64  `json:"expiry_time"`
}

func (n *Node) ConfigIntent() NodeConfigIntent {
	if n == nil {
		return NodeConfigIntent{}
	}
	return NodeConfigIntent{
		Enabled: n.Enabled, Listen: n.InboundListen, Port: n.DesiredPort,
		Protocol: n.DesiredProtocol, Remark: n.InboundRemark,
		Settings: n.InboundSettings, StreamSettings: n.StreamSettings,
		Sniffing: n.Sniffing, Allocate: n.Allocate, ExpiryTime: n.InboundExpiryTime,
	}
}
