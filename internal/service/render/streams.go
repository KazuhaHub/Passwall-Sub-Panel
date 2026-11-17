package render

// Partial JSON shapes that mirror what 3X-UI stores in inbound.settings and
// inbound.streamSettings. We decode only the fields the renderer cares
// about; anything else passes through silently.

type xuiInboundSettings struct {
	// SS / SS-2022 — settings.method / settings.password are root-level
	// in 3X-UI's ShadowsocksSettings model.
	Method   string `json:"method"`
	Password string `json:"password"`
	Network  string `json:"network"`

	PaddingScheme         []string `json:"padding_scheme"`
	CongestionControl     string   `json:"congestion_control"`
	AuthTimeout           string   `json:"auth_timeout"`
	ZeroRTTHandshake      bool     `json:"zero_rtt_handshake"`
	Heartbeat             string   `json:"heartbeat"`
	QUICCongestionControl string   `json:"quic_congestion_control"`
}

type xuiStreamSettings struct {
	Network         string              `json:"network"`
	Security        string              `json:"security"`
	RealitySettings *xuiRealitySettings `json:"realitySettings"`
	TLSSettings     *xuiTLSSettings     `json:"tlsSettings"`
	WSSettings      *xuiWSSettings      `json:"wsSettings"`
	GRPCSettings    *xuiGRPCSettings    `json:"grpcSettings"`
	// HTTPUpgradeSettings is the HTTPUpgrade transport (path + Host).
	HTTPUpgradeSettings *xuiHTTPUpgradeSettings `json:"httpupgradeSettings"`
	// Finalmask is the xray-core extension where 3X-UI stores Hysteria 2
	// salamander obfs (under .udp[] as type=salamander). Documented in
	// frontend/src/models/inbound.js FinalMaskStreamSettings.
	FinalMask *xuiFinalMask `json:"finalmask,omitempty"`

	// cdn marks the stream of a CDN line (effectiveEndpoint sets it): the
	// builders then also carry the landing's TLS fingerprint, which the client
	// presents to the CDN.
	cdn bool
}

// cdnFingerprint is the TLS fingerprint a CDN line's client presents: the
// landing's own, and empty for every other entry (whose output is unchanged).
func (s xuiStreamSettings) cdnFingerprint() string {
	if !s.cdn || s.TLSSettings == nil {
		return ""
	}
	return s.TLSSettings.Settings.Fingerprint
}

type xuiFinalMask struct {
	TCP []xuiMaskEntry `json:"tcp"`
	UDP []xuiMaskEntry `json:"udp"`
}

type xuiMaskEntry struct {
	Type     string         `json:"type"`
	Settings map[string]any `json:"settings"`
}

type xuiRealitySettings struct {
	Show        bool     `json:"show"`
	Xver        int      `json:"xver"`
	Dest        string   `json:"dest"`
	ServerNames []string `json:"serverNames"`
	PrivateKey  string   `json:"privateKey"`
	ShortIds    []string `json:"shortIds"`
	Settings    struct {
		PublicKey   string `json:"publicKey"`
		Fingerprint string `json:"fingerprint"`
		ServerName  string `json:"serverName"`
		SpiderX     string `json:"spiderX"`
	} `json:"settings"`
}

type xuiTLSSettings struct {
	ServerName string   `json:"serverName"`
	ALPN       []string `json:"alpn"`
	// AllowInsecure mirrors 3X-UI's tlsSettings.allowInsecure — when true the
	// client must skip cert verification (self-signed / mismatched SNI). Maps
	// to skip-cert-verify (Clash), insecure (sing-box), allowInsecure (URI).
	AllowInsecure bool `json:"allowInsecure"`
	Settings      struct {
		Fingerprint string `json:"fingerprint"`
	} `json:"settings"`
}

type xuiWSSettings struct {
	Path string `json:"path"`
	// Host is Xray's own wsSettings.host, which a headers.Host overrides.
	Host    string            `json:"host"`
	Headers map[string]string `json:"headers"`
}

type xuiGRPCSettings struct {
	ServiceName string `json:"serviceName"`
	// MultiMode is Xray's multi mode (otherwise gun).
	MultiMode bool `json:"multiMode"`
	// Authority is the gRPC :authority (xray's grpcSettings.authority).
	Authority string `json:"authority"`
}

type xuiHTTPUpgradeSettings struct {
	Path    string            `json:"path"`
	Host    string            `json:"host"`
	Headers map[string]string `json:"headers"`
}

// host is the Host an HTTPUpgrade client sends: the host field, else the Host
// header.
func (h xuiHTTPUpgradeSettings) host() string {
	if h.Host != "" {
		return h.Host
	}
	return h.Headers["Host"]
}

// normalizeHosts puts the Host that Xray's own host fields name into the header
// the builders read, unless the header is set.
func (s *xuiStreamSettings) normalizeHosts() {
	if w := s.WSSettings; w != nil && w.Host != "" && w.Headers["Host"] == "" {
		if w.Headers == nil {
			w.Headers = map[string]string{}
		}
		w.Headers["Host"] = w.Host
	}
}
