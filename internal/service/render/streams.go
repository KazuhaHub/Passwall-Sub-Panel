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
}

type xuiStreamSettings struct {
	Network         string              `json:"network"`
	Security        string              `json:"security"`
	RealitySettings *xuiRealitySettings `json:"realitySettings"`
	TLSSettings     *xuiTLSSettings     `json:"tlsSettings"`
	WSSettings      *xuiWSSettings      `json:"wsSettings"`
	GRPCSettings    *xuiGRPCSettings    `json:"grpcSettings"`
	// Finalmask is the xray-core extension where 3X-UI stores Hysteria 2
	// salamander obfs (under .udp[] as type=salamander). Documented in
	// frontend/src/models/inbound.js FinalMaskStreamSettings.
	FinalMask *xuiFinalMask `json:"finalmask,omitempty"`
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
	ServerName string `json:"serverName"`
	ALPN       []string `json:"alpn"`
	// AllowInsecure mirrors 3X-UI's tlsSettings.allowInsecure — when true the
	// client must skip cert verification (self-signed / mismatched SNI). Maps
	// to skip-cert-verify (Clash), insecure (sing-box), allowInsecure (URI).
	AllowInsecure bool `json:"allowInsecure"`
}

type xuiWSSettings struct {
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
}

type xuiGRPCSettings struct {
	ServiceName string `json:"serviceName"`
}
