package domain

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strconv"
	"strings"
)

// Per-line client identity (ADR 0043).
//
// A user holds one client per subscription LINE they can see: a node's direct
// line and each of its relay lines. The line is what the administrator prices
// (its traffic factor), so it is what has to own a counter: a relay that only
// forwards to the landing's listener would otherwise report into the direct
// line's counter, and its traffic could not be charged at its own factor.
//
// The direct line keeps the user's own UUID and the legacy password classes,
// so a user who only uses direct lines sees no change in any subscription. Every
// other line gets an identity derived from the user's UUID and the line, stored
// on the row, and unique on the listener it lives on.

// LineKey names one subscription line: a node and one of its lines.
// LineID is DirectLineID for the node's own endpoint, a RelayLine.ID otherwise.
type LineKey struct {
	NodeID int64
	LineID string
}

// DirectLine is the key of a node's direct line.
func DirectLine(nodeID int64) LineKey { return LineKey{NodeID: nodeID, LineID: DirectLineID} }

// IsDirect reports whether this is a node's direct line.
func (k LineKey) IsDirect() bool { return k.LineID == DirectLineID }

// lineIdentityDomain separates this derivation from any other HMAC keyed by a
// user UUID. Changing it re-keys every non-direct line of every user.
const lineIdentityDomain = "psp-line-v1"

// DeriveLineUUID is the client UUID of a non-direct line: the first 16 bytes
// of HMAC-SHA256 keyed by the user's UUID over the line, shaped as a random
// (version 4, RFC 4122 variant) UUID so every panel and core accepts it.
// attempt 1 is the identity; a later attempt is used only when an earlier one
// collides on the listener (see LineUUIDKey).
func DeriveLineUUID(userUUID string, key LineKey, attempt int) string {
	msg := lineIdentityDomain + "\x1f" + strconv.FormatInt(key.NodeID, 10) + "\x1f" + key.LineID
	if attempt > 1 {
		msg += "#" + strconv.Itoa(attempt)
	}
	mac := hmac.New(sha256.New, []byte(userUUID))
	mac.Write([]byte(msg))
	b := mac.Sum(nil)[:16]
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// LineUUIDKey is the form two client UUIDs are compared in for uniqueness on
// one listener: lower-case hex with bytes 6–7 masked. A VLESS server matches
// the 16 UUID bytes, but some clients and panels rewrite the version nibble, so
// two identities that differ only there are treated as the same.
func LineUUIDKey(uuid string) string {
	h := strings.ToLower(strings.ReplaceAll(uuid, "-", ""))
	if len(h) != 32 {
		return h
	}
	return h[:12] + "0000" + h[16:]
}

// LineEmailSuffix is the email suffix of a line's client row created from 4.3
// on: "-l" + the first 10 hex digits of sha256("{nodeID}|{lineID}"). Rows that
// already exist keep the email they have.
func LineEmailSuffix(key LineKey) string {
	sum := sha256.Sum256([]byte(strconv.FormatInt(key.NodeID, 10) + "|" + key.LineID))
	return "-l" + hex.EncodeToString(sum[:])[:10]
}

// CountsSourceIPs reports whether the source addresses this client's server
// sees are the person's own. A relay line forwards to the landing's listener,
// so the landing sees the relay's address for every user of the line: those
// addresses say nothing about how many places a person connects from and are
// left out of the person-level IP and location counts. The direct line and a
// legacy row count. A tunnel-mode line is served by a listener of its own that
// terminates the user's connection, so its rows count too, but a row alone
// cannot tell it is one (the line is the landing's, the row sits on another
// server): the traffic service decides by the landing's lines (tunnelLineRows).
func (c *PSPClient) CountsSourceIPs() bool {
	line, ok := c.Line()
	return !ok || line.IsDirect()
}

// LineSetChanged reports whether an edit changed which relay lines can carry a
// client (ADR 0043) or the labels a group's line filter reads (ADR 0041): when
// it did, the users who may see the node need their clients re-planned.
func LineSetChanged(before, after []RelayLine) bool {
	if len(before) != len(after) {
		return true
	}
	for i := range before {
		b, a := before[i], after[i]
		if b.ID != a.ID || b.Enabled != a.Enabled || b.Region != a.Region || !slices.Equal(b.Tags, a.Tags) ||
			b.Mode != a.Mode || b.TunnelNodeID != a.TunnelNodeID {
			return true
		}
	}
	return false
}
