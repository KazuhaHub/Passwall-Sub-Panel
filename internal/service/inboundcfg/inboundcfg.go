// Package inboundcfg maps between the 3X-UI inbound representation
// (ports.Inbound / ports.InboundSpec) and a node's locally stored config
// snapshot. v3.5 makes PSP the source of truth for the inbound connection
// config: the node service writes the snapshot on create/import/update, the
// renderer reads it (zero live fetch), and reconcile pushes it back over
// server-side drift. Shared here so the mapping lives in exactly one place.
// See docs/inbound-ownership.md.
package inboundcfg

import (
	"encoding/json"
	"reflect"
	"strings"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// StripClients removes the clients[] array from a 3X-UI inbound settings JSON,
// leaving the protocol-level config (SS/SS-2022 method+password, VLESS/VMess
// decryption/fallbacks, …). The snapshot is stored client-less because clients
// are owned by the ownership table and re-materialised at push time; carrying
// a client list would let it go stale and risks clobbering manually-created
// clients. Non-object or malformed input is returned verbatim — store what we
// got rather than silently lose it.
//
// Note: when clients[] IS present, the unmarshal+marshal round-trip renormalises
// the remaining keys (sorted, default whitespace), so the returned string is
// not byte-identical to the input. InSync uses semantic JSON compare so this
// doesn't register as drift; just be aware that "stored snapshot" is not
// guaranteed to match the live wire format byte-for-byte.
func StripClients(settingsJSON string) string {
	if strings.TrimSpace(settingsJSON) == "" {
		return settingsJSON
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(settingsJSON), &m); err != nil {
		return settingsJSON
	}
	if _, ok := m["clients"]; !ok {
		return settingsJSON
	}
	delete(m, "clients")
	out, err := json.Marshal(m)
	if err != nil {
		return settingsJSON
	}
	return string(out)
}

// ApplySpec writes an admin-supplied InboundSpec into the node's local config
// snapshot (write-through on create / update) and marks it synced.
//
// Field semantics (intentional, locked here so the contract is testable):
//   - Listen / Remark / Settings / StreamSettings / Sniffing / Allocate /
//     ExpiryTime are written UNCONDITIONALLY (full-replace). Today the only
//     caller is the admin's inbound-edit form which always submits the full
//     config; adding a partial-PATCH route would zero unspecified fields, so
//     keep this in mind if extending the write surface.
//   - Port and Protocol are guarded by zero/empty: spec.Port==0 or
//     spec.Protocol=="" leaves the existing node value alone. This is the
//     one asymmetric pair, deliberately, because both have meaningful
//     defaults the form may omit during a partial save.
//   - Enable is NOT carried into the snapshot — n.Enabled is admin-owned
//     and lives on a separate axis (SetInboundEnable / SyncTaskNodeSetEnabled).
//   - clients[] is stripped from Settings before storage; the RMW push at
//     UpdateInbound time re-merges whatever live clients 3X-UI has.
func ApplySpec(n *domain.Node, spec ports.InboundSpec) {
	n.InboundListen = spec.Listen
	n.InboundRemark = spec.Remark
	n.InboundSettings = normalizeSettings(StripClients(spec.Settings))
	n.StreamSettings = spec.StreamSettings
	n.Sniffing = spec.Sniffing
	n.Allocate = spec.Allocate
	n.InboundExpiryTime = spec.ExpiryTime
	if spec.Port != 0 {
		n.Port = spec.Port
	}
	if p := strings.ToLower(spec.Protocol); p != "" {
		n.Protocol = p
	}
	markSynced(n)
}

// Capture writes a live 3X-UI inbound into the node's local config snapshot
// (import = take ownership; reconcile backfill / post-push convergence) and
// marks it synced.
func Capture(n *domain.Node, inb *ports.Inbound) {
	n.InboundListen = inb.Listen
	n.InboundRemark = inb.Remark
	n.InboundSettings = normalizeSettings(StripClients(inb.Settings))
	n.StreamSettings = inb.StreamSettings
	n.Sniffing = inb.Sniffing
	n.Allocate = inb.Allocate
	n.InboundExpiryTime = inb.ExpiryTime
	if inb.Port != 0 {
		n.Port = inb.Port
	}
	if p := strings.ToLower(inb.Protocol); p != "" {
		n.Protocol = p
	}
	markSynced(n)
}

// normalizeSettings substitutes "{}" for blank input so the snapshot is always
// syntactically valid JSON. A blank settings string would otherwise survive
// drift comparison as a perpetual mismatch and — worse — would be pushed
// verbatim to 3X-UI, where the RMW client-preservation path bails on empty
// input. Storing {} forces every downstream consumer through the merge logic.
func normalizeSettings(s string) string {
	if strings.TrimSpace(s) == "" {
		return "{}"
	}
	return s
}

// SpecFromNode builds the InboundSpec reconcile pushes to 3X-UI from the node
// snapshot. Settings carry no clients[]; XUIClient.UpdateInbound's read-modify-
// write re-merges whatever clients are live, preserving PSP-managed and
// manually-created clients alike.
func SpecFromNode(n *domain.Node) ports.InboundSpec {
	return ports.InboundSpec{
		Remark:         n.InboundRemark,
		Enable:         n.Enabled,
		Listen:         n.InboundListen,
		Port:           n.Port,
		Protocol:       n.Protocol,
		Settings:       n.InboundSettings,
		StreamSettings: n.StreamSettings,
		Sniffing:       n.Sniffing,
		Allocate:       n.Allocate,
		ExpiryTime:     n.InboundExpiryTime,
	}
}

// InboundFromNode reconstructs a ports.Inbound for the renderer from the node
// snapshot. clients[] is intentionally absent: render derives each user's
// credential from user.uuid and never consults the inbound client list.
//
// Enable is filled from n.Enabled — PSP's view of "is this node serving
// users", not 3X-UI's view. They can diverge if an operator disables the
// inbound directly in 3X-UI. The health probe catches this at the data
// plane (closed port → Down), and reconcile's SpecFromNode push carries
// the same Enable bit back, so any direct 3X-UI flip self-heals on the
// next reconcile cycle. ports.Inbound.Tag (xray-internal identifier) is
// not part of the snapshot and isn't reconstructed; PSP doesn't render
// or push it.
func InboundFromNode(n *domain.Node) *ports.Inbound {
	return &ports.Inbound{
		ID:             n.InboundID,
		Enable:         n.Enabled,
		ExpiryTime:     n.InboundExpiryTime,
		Listen:         n.InboundListen,
		Remark:         n.InboundRemark,
		Port:           n.Port,
		Protocol:       n.Protocol,
		Settings:       n.InboundSettings,
		StreamSettings: n.StreamSettings,
		Sniffing:       n.Sniffing,
		Allocate:       n.Allocate,
	}
}

// InSync reports whether a live 3X-UI inbound already matches the node's stored
// config on the fields PSP owns. clients[] is ignored (compared with clients
// stripped) and JSON is compared semantically so key ordering / whitespace
// don't register as drift. A false result means reconcile should push.
//
// It need not be perfect: reconcile re-captures the live config after pushing,
// so a borderline mismatch (e.g. 3X-UI normalising JSON) self-corrects after a
// single push instead of looping.
func InSync(n *domain.Node, live *ports.Inbound) bool {
	if n.Port != live.Port {
		return false
	}
	if !strings.EqualFold(n.Protocol, live.Protocol) {
		return false
	}
	if n.InboundListen != live.Listen {
		return false
	}
	// InboundRemark is intentionally NOT compared: it's a cosmetic label, and
	// enforcing it would make reconcile revert an admin's direct 3X-UI rename
	// every cycle. The remark still rides along in SpecFromNode, so a push
	// triggered by a real config drift carries PSP's remark, but a remark-only
	// change never triggers one. See docs/inbound-ownership.md.
	if n.InboundExpiryTime != live.ExpiryTime {
		return false
	}
	if !jsonEqual(n.StreamSettings, live.StreamSettings) {
		return false
	}
	if !jsonEqual(n.Sniffing, live.Sniffing) {
		return false
	}
	if !jsonEqual(n.Allocate, live.Allocate) {
		return false
	}
	return jsonEqual(StripClients(n.InboundSettings), StripClients(live.Settings))
}

func markSynced(n *domain.Node) {
	now := time.Now()
	n.ConfigSyncedAt = &now
	n.ConfigSyncState = "synced"
}

// jsonEqual compares two JSON strings semantically: key ordering and whitespace
// don't matter. All "effectively empty" forms — "", "null", "{}", "[]" — compare
// equal to each other, so the asymmetry between a stored snapshot normalised to
// "{}" and a live value 3X-UI returns as "" / null can't register as perpetual
// drift. Unparseable input on either side (and not a blank) falls back to a
// trimmed string comparison.
func jsonEqual(a, b string) bool {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if a == b {
		return true
	}
	av, aok := parseJSONLoose(a)
	bv, bok := parseJSONLoose(b)
	if !aok || !bok {
		return false
	}
	if isEmptyJSON(av) && isEmptyJSON(bv) {
		return true
	}
	return reflect.DeepEqual(av, bv)
}

// parseJSONLoose unmarshals s, treating a blank string as JSON null (rather than
// a parse error) so blank/"null" normalise together.
func parseJSONLoose(s string) (any, bool) {
	if s == "" {
		return nil, true
	}
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return nil, false
	}
	return v, true
}

// isEmptyJSON reports whether v carries no meaningful config: JSON null, an
// empty object, or an empty array.
func isEmptyJSON(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case map[string]any:
		return len(t) == 0
	case []any:
		return len(t) == 0
	default:
		return false
	}
}
