package render

// Partial JSON shapes that mirror what 3X-UI stores in inbound.settings and
// inbound.streamSettings. We decode only the fields the renderer cares
// about; anything else passes through silently.

type xuiInboundSettings struct {
	// SS / SS-2022
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
}

type xuiWSSettings struct {
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
}

type xuiGRPCSettings struct {
	ServiceName string `json:"serviceName"`
}
