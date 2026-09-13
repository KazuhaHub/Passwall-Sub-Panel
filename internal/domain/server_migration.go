package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ServerMigrationSnapshot is the persisted, PSP-managed configuration closure.
// It does not claim to represent an upstream panel's global configuration or
// operator-created clients. Never serialize this secret-bearing type to HTTP.
type ServerMigrationSnapshot struct {
	Panel                *Panel
	Nodes                []*Node
	Clients              []ServerMigrationClient
	LegacyOwnershipCount int64
}

type ServerMigrationClient struct {
	Client   *PSPClient
	Inbounds []PSPClientInbound
}

// MigrationIssue carries identifiers, never raw configuration or credentials.
type MigrationIssue struct {
	Code     string `json:"code"`
	NodeID   int64  `json:"node_id,omitempty"`
	ClientID int64  `json:"client_id,omitempty"`
}

type ServerMigrationPreview struct {
	ServerID               int64            `json:"server_id"`
	ServerName             string           `json:"server_name"`
	CoreVersion            string           `json:"core_version"`
	RecommendedCoreVersion string           `json:"recommended_core_version"`
	CoreRequiresAck        bool             `json:"core_requires_ack"`
	AllowRestrictedReality bool             `json:"allow_restricted_reality"`
	Fingerprint            string           `json:"fingerprint"`
	NodeCount              int              `json:"node_count"`
	ClientCount            int              `json:"client_count"`
	Blockers               []MigrationIssue `json:"blockers"`
	Warnings               []MigrationIssue `json:"warnings"`
	CanMigrate             bool             `json:"can_migrate"`
}

// Fingerprint binds the administrator's exact core choice to the configuration
// closure, without exposing any secret-bearing preimage. Traffic and health
// observations must not invalidate an otherwise unchanged maintenance preview.
// Config stamps and confirmed credentials are deliberately retained: a preview
// is not permission to migrate a later, unconfirmed rollout.
func (s *ServerMigrationSnapshot) Fingerprint(coreVersion string, allowRestrictedReality bool) string {
	var panel *Panel
	var nodes, clients []string
	var legacy int64
	if s != nil {
		legacy = s.LegacyOwnershipCount
		if s.Panel != nil {
			p := *s.Panel
			p.VersionCheckedAt, p.IPLimitProbedAt = nil, nil
			p.IPLimitEnforcement = ""
			panel = &p
		}
		for _, original := range s.Nodes {
			if original == nil {
				nodes = append(nodes, "null")
				continue
			}
			n := *original
			n.CreatedAt = time.Time{}
			n.LifetimeUpBytes, n.LifetimeDownBytes, n.LifetimeTotalBytes = 0, 0, 0
			n.LastTrafficUpBytes, n.LastTrafficDownBytes, n.LastTrafficTotalBytes = 0, 0, 0
			n.LastInboundUpBytes, n.LastInboundDownBytes, n.LastInboundTotalBytes = 0, 0, 0
			n.LastInboundCounterEpoch, n.LastInboundSeeded = 0, false
			n.HealthState, n.HealthDetail, n.HealthCheckedAt, n.RelayHealth = "", "", nil, nil
			n.InboundSettings, n.StreamSettings = canonicalMigrationJSON(n.InboundSettings), canonicalMigrationJSON(n.StreamSettings)
			n.Sniffing, n.Allocate = canonicalMigrationJSON(n.Sniffing), canonicalMigrationJSON(n.Allocate)
			encoded, _ := json.Marshal(n)
			nodes = append(nodes, string(encoded))
		}
		for _, original := range s.Clients {
			var client *PSPClient
			if original.Client != nil {
				c := *original.Client
				c.CreatedAt = time.Time{}
				c.LifetimeUpBytes, c.LifetimeDownBytes, c.LifetimeTotalBytes = 0, 0, 0
				c.LastRawUpBytes, c.LastRawDownBytes, c.LastRawTotalBytes = 0, 0, 0
				c.PeriodBaselineUpBytes, c.PeriodBaselineDownBytes, c.PeriodBaselineTotalBytes = 0, 0, 0
				c.LastCounterEpoch = 0
				// This traffic-derived floor is a legacy upstream projection;
				// native directives are minted independently after conversion.
				c.PanelQuotaHeadroom = 0
				client = &c
			}
			attachments := make([]string, 0, len(original.Inbounds))
			for _, a := range original.Inbounds {
				a.FirstFailedAt = nil
				encoded, _ := json.Marshal(a)
				attachments = append(attachments, string(encoded))
			}
			sort.Strings(attachments)
			encoded, _ := json.Marshal(struct {
				Client      *PSPClient
				Attachments []string
			}{client, attachments})
			clients = append(clients, string(encoded))
		}
	}
	sort.Strings(nodes)
	sort.Strings(clients)
	encoded, _ := json.Marshal(struct {
		Schema                 int
		CoreVersion            string
		AllowRestrictedReality bool
		Panel                  *Panel
		Nodes, Clients         []string
		Legacy                 int64
	}{1, coreVersion, allowRestrictedReality, panel, nodes, clients, legacy})
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

// Blockers checks persisted PSP intent, including disabled listeners. The PN
// compiler skips validation of disabled inbounds; migration must not hide an
// unsupported configuration until the administrator enables it later.
func (s *ServerMigrationSnapshot) Blockers() []MigrationIssue {
	issues := make([]MigrationIssue, 0)
	add := func(code string, nodeID, clientID int64) {
		issues = append(issues, MigrationIssue{Code: code, NodeID: nodeID, ClientID: clientID})
	}
	if s == nil || s.Panel == nil {
		add("missing_snapshot", 0, 0)
		return issues
	}
	if s.Panel.ID <= 0 || NormalizePanelKind(s.Panel.Kind) != PanelKind3XUI {
		add("source_not_3xui", 0, 0)
	}
	if s.LegacyOwnershipCount != 0 {
		add("legacy_ownership_pending", 0, 0)
	}
	if len(s.Nodes) == 0 {
		add("missing_snapshot", 0, 0)
	}
	nodes := make(map[int64]*Node, len(s.Nodes))
	inbounds := make(map[int]int64, len(s.Nodes))
	bindings := make(map[string]bool, len(s.Nodes))
	for _, n := range s.Nodes {
		if n == nil {
			add("missing_snapshot", 0, 0)
			continue
		}
		if n.ID <= 0 || n.InboundID <= 0 || n.IsSeparator() {
			add("missing_snapshot", n.ID, 0)
		}
		if _, exists := nodes[n.ID]; exists {
			add("duplicate_node", n.ID, 0)
		}
		nodes[n.ID] = n
		if previous, exists := inbounds[n.InboundID]; exists && previous != n.ID {
			add("duplicate_inbound", n.ID, 0)
		}
		inbounds[n.InboundID] = n.ID
		if n.PanelID != s.Panel.ID {
			add("cross_panel_attachment", n.ID, 0)
		}
		if n.ConfigSyncedAt == nil || n.ConfigSyncedAt.IsZero() {
			add("missing_snapshot", n.ID, 0)
		}
		if n.ConfigSyncState != ConfigSyncSynced || n.ConfigPendingSince != nil {
			add("config_not_synced", n.ID, 0)
		}
		if n.DesiredPort < 1 || n.DesiredPort > 65535 || strings.TrimSpace(n.DesiredProtocol) == "" || !n.EndpointInSync() {
			add("endpoint_not_confirmed", n.ID, 0)
		}
		p := strings.ToLower(strings.TrimSpace(n.DesiredProtocol))
		switch p {
		case "vless", "vmess", "trojan", "shadowsocks":
		default:
			add("unsupported_protocol", n.ID, 0)
		}
		if n.InboundExpiryTime != 0 {
			// PN accepts expiry_time in its DTO but does not enforce it.
			add("inbound_expiry_unsupported", n.ID, 0)
		}
		if n.Enabled {
			host := strings.TrimSpace(n.InboundListen)
			if host == "" {
				host = "0.0.0.0"
			}
			binding := net.JoinHostPort(host, strconv.Itoa(n.DesiredPort))
			if bindings[binding] {
				add("duplicate_listener_binding", n.ID, 0)
			}
			bindings[binding] = true
		}
		if n.Flow != "" && n.Flow != "xtls-rprx-vision" {
			add("unsupported_flow", n.ID, 0)
		}
		for i, raw := range []string{n.InboundSettings, n.StreamSettings, n.Sniffing, n.Allocate} {
			if i > 0 && strings.TrimSpace(raw) == "" {
				continue
			}
			object, err := migrationJSONObject(raw)
			if err != nil {
				add("invalid_config", n.ID, 0)
				continue
			}
			if i == 1 && unsafeMigrationRealityFinalmaskTCP(object) {
				// The old 3X-UI push strips this combination, while PN forwards
				// streamSettings verbatim. Xray 26.6.27 (a selectable release)
				// panics on its first REALITY connection with a TCP mask
				// (XTLS/Xray-core#6453). Do not silently restore a hazardous
				// stale snapshot, or silently delete the administrator's intent.
				add("unsafe_reality_finalmask_tcp", n.ID, 0)
			}
			if _, exists := object["clients"]; exists {
				add("snapshot_contains_clients", n.ID, 0)
			}
			for _, code := range migrationConfigDependencies(object) {
				add(code, n.ID, 0)
			}
		}
	}
	clientIDs, usernames := make(map[int64]bool), make(map[string]bool)
	for _, record := range s.Clients {
		c := record.Client
		if c == nil {
			add("missing_client", 0, 0)
			for _, a := range record.Inbounds {
				add("orphan_attachment", a.NodeID, a.ClientID)
			}
			continue
		}
		if c.ID <= 0 || c.UserID <= 0 {
			add("missing_client", 0, c.ID)
		}
		if clientIDs[c.ID] {
			add("duplicate_client", 0, c.ID)
		}
		clientIDs[c.ID] = true
		if c.PanelID != s.Panel.ID {
			add("cross_panel_attachment", 0, c.ID)
		}
		if !c.DesiredMinted || c.DesiredExpiryTime < 0 || c.PanelQuotaHeadroom < 0 || c.PanelIPLimit < 0 || c.PanelDeviceLimit < 0 {
			add("lifecycle_not_minted", 0, c.ID)
		}
		username := strings.TrimSpace(c.Email)
		if username == "" {
			add("invalid_credentials", 0, c.ID)
		}
		if usernames[username] {
			add("duplicate_username", 0, c.ID)
		}
		usernames[username] = true
		if len(record.Inbounds) == 0 {
			add("missing_attachment", 0, c.ID)
		}
		seen := make(map[int64]bool)
		flow := ""
		flowSet := false
		for _, a := range record.Inbounds {
			if a.ClientID != c.ID {
				add("orphan_attachment", a.NodeID, c.ID)
			}
			if seen[a.NodeID] {
				add("duplicate_attachment", a.NodeID, c.ID)
			}
			seen[a.NodeID] = true
			n, found := nodes[a.NodeID]
			if !found {
				add("orphan_attachment", a.NodeID, c.ID)
				continue
			}
			if n.PanelID != c.PanelID {
				add("cross_panel_attachment", a.NodeID, c.ID)
			}
			if !a.Applied() || a.AppliedEmail != c.Email || a.AppliedUUID != c.UUID || a.AppliedPassword != c.Password {
				add("credential_not_confirmed", a.NodeID, c.ID)
			}
			p := strings.ToLower(strings.TrimSpace(n.DesiredProtocol))
			if a.FlowOverride != "" && a.FlowOverride != "xtls-rprx-vision" {
				add("unsupported_flow", a.NodeID, c.ID)
			}
			// PN has one flow slot per roster client. Mixing empty and Vision
			// VLESS attachments would silently enable Vision on both listeners.
			if p == "vless" {
				if flowSet && flow != a.FlowOverride {
					add("flow_conflict", a.NodeID, c.ID)
				}
				flow, flowSet = a.FlowOverride, true
			}
			if ((p == "vless" || p == "vmess") && strings.TrimSpace(c.UUID) == "") || ((p == "trojan" || p == "shadowsocks") && c.Password == "") {
				add("invalid_credentials", a.NodeID, c.ID)
			}
		}
	}
	return uniqueMigrationIssues(issues)
}

// Warnings exposes environmental dependencies without echoing their values.
// Transport configuration is forwarded, not blanket-rejected by network name.
func (s *ServerMigrationSnapshot) Warnings() []MigrationIssue {
	issues := make([]MigrationIssue, 0)
	if s == nil {
		return issues
	}
	for _, n := range s.Nodes {
		if n == nil {
			continue
		}
		for _, raw := range []string{n.InboundSettings, n.StreamSettings} {
			object, err := migrationJSONObject(raw)
			if err != nil {
				continue
			}
			walkMigrationJSON(object, func(key string, value any) {
				if key == "fallbacks" && migrationNonempty(value) {
					issues = append(issues, MigrationIssue{Code: "fallback_environment_dependency", NodeID: n.ID})
				}
				if key == "sockopt" && migrationNonempty(value) {
					issues = append(issues, MigrationIssue{Code: "socket_environment_dependency", NodeID: n.ID})
				}
			})
		}
	}
	for _, c := range s.Clients {
		if c.Client != nil && (c.Client.PanelIPLimit > 0 || c.Client.PanelDeviceLimit > 0) {
			issues = append(issues, MigrationIssue{Code: "connection_limits_not_enforced", ClientID: c.Client.ID})
		}
	}
	return uniqueMigrationIssues(issues)
}

func migrationJSONObject(raw string) (map[string]any, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil {
		return nil, err
	}
	if object == nil {
		return nil, errors.New("configuration must be a JSON object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("configuration has trailing data")
	}
	return object, nil
}

func canonicalMigrationJSON(raw string) string {
	object, err := migrationJSONObject(raw)
	if err != nil {
		return raw
	}
	encoded, _ := json.Marshal(object)
	return string(encoded)
}

func unsafeMigrationRealityFinalmaskTCP(stream map[string]any) bool {
	security, _ := stream["security"].(string)
	if !strings.EqualFold(strings.TrimSpace(security), "reality") {
		return false
	}
	finalmask, _ := stream["finalmask"].(map[string]any)
	tcp, _ := finalmask["tcp"].([]any)
	return len(tcp) > 0
}

func migrationConfigDependencies(object map[string]any) []string {
	codes := make([]string, 0)
	walkMigrationJSON(object, func(key string, value any) {
		switch key {
		case "certificatefile", "keyfile", "certfile", "privatekeyfile", "cafile", "cacertfile", "crlfile", "clientcertificatefile", "clientkeyfile":
			if migrationNonempty(value) {
				codes = append(codes, "external_file_dependency")
			}
		case "routing", "outbounds", "dns", "reverse", "balancers", "observatory", "dialerproxy":
			if migrationNonempty(value) {
				codes = append(codes, "global_config_dependency")
			}
		case "fallbacks":
			fallbacks, ok := value.([]any)
			if !ok {
				if migrationNonempty(value) {
					codes = append(codes, "invalid_config")
				}
				return
			}
			for _, item := range fallbacks {
				fallback, ok := item.(map[string]any)
				if !ok {
					codes = append(codes, "invalid_config")
					continue
				}
				if migrationLocalDestination(fallback["dest"]) {
					codes = append(codes, "local_fallback_dependency")
				}
			}
		}
	})
	return codes
}

func migrationLocalDestination(value any) bool {
	if value == nil {
		return true
	}
	if _, ok := value.(json.Number); ok {
		return true
	}
	text, ok := value.(string)
	if !ok {
		return true
	}
	text = strings.TrimSpace(text)
	if text == "" || strings.HasPrefix(text, "/") || strings.HasPrefix(text, "@") || strings.HasPrefix(strings.ToLower(text), "unix:") {
		return true
	}
	if _, err := strconv.Atoi(text); err == nil {
		return true
	}
	host, _, err := net.SplitHostPort(text)
	if err != nil {
		return true
	}
	if host == "" || strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsUnspecified() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast())
}

func walkMigrationJSON(value any, visit func(string, any)) {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			visit(strings.ToLower(key), item)
			walkMigrationJSON(item, visit)
		}
	case []any:
		for _, item := range v {
			walkMigrationJSON(item, visit)
		}
	}
}

func migrationNonempty(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(v) != ""
	case []any:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	default:
		return true
	}
}

func uniqueMigrationIssues(issues []MigrationIssue) []MigrationIssue {
	sort.Slice(issues, func(i, j int) bool {
		if issues[i].Code != issues[j].Code {
			return issues[i].Code < issues[j].Code
		}
		if issues[i].NodeID != issues[j].NodeID {
			return issues[i].NodeID < issues[j].NodeID
		}
		return issues[i].ClientID < issues[j].ClientID
	})
	result := make([]MigrationIssue, 0, len(issues))
	for _, issue := range issues {
		if len(result) == 0 || result[len(result)-1] != issue {
			result = append(result, issue)
		}
	}
	return result
}
