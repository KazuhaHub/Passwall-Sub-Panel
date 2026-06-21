package render

import (
	"context"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/crypto"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// credPlan maps a node to the STORED v3.9.0 shared-client password render should
// emit for it (cutover Stage 2). Built once per render and only when the
// SubRenderUseSharedClient gate is on; nil otherwise — in which case render keeps
// deriving per-node passwords from the UUID, byte-identical to pre-v3.9.0.
//
// Only PASSWORD protocols are affected: VLESS/VMess use the id (= user UUID),
// Hysteria2 uses auth (= user UUID), and the shared client carries that same
// UUID — so those emit identically with or without the gate. Trojan/SS change
// from the UUID-derived value to the stored 32-byte value at flip (the documented
// migration cost — users re-fetch their sub); SS-2022 is stored-equals-derived.
type credPlan struct {
	byNode map[int64]string // nodeID -> stored psp_client.Password
}

// buildCredPlan returns nil when the cutover gate is OFF (legacy derive path).
// When ON it prefetches the user's psp_clients + attachments into a nodeID→
// password map. Only attachments CONFIRMED provisioned in 3X-UI are mapped — an
// un-provisioned node's shared client isn't live, so it must keep rendering its
// legacy per-node derived credential (that client still exists until Stage 4).
// Any load failure degrades safely to the derived path.
func (s *Service) buildCredPlan(ctx context.Context, u *domain.User, st ports.UISettings) *credPlan {
	if !st.SubRenderUseSharedClient || u == nil || s.repos.PSPClient == nil {
		return nil
	}
	clients, err := s.repos.PSPClient.ListByUser(ctx, u.ID)
	if err != nil {
		log.Warn("render: shared-client cred plan load failed; using derived passwords", "user_id", u.ID, "err", err)
		return nil
	}
	byNode := make(map[int64]string)
	for _, c := range clients {
		atts, err := s.repos.PSPClient.ListInbounds(ctx, c.ID)
		if err != nil {
			log.Warn("render: shared-client attachments load failed; node falls back to derived", "client_id", c.ID, "err", err)
			continue
		}
		for _, a := range atts {
			if a.Provisioned && c.Password != "" {
				byNode[a.NodeID] = c.Password
			}
		}
	}
	if len(byNode) == 0 {
		return nil
	}
	return &credPlan{byNode: byNode}
}

// password is the password render should emit for a password-protocol node: the
// stored shared-client value when this node is provisioned under the gate, else
// the legacy UUID-derived value (gate off OR node not yet provisioned). Safe to
// call on a nil *credPlan.
func (cp *credPlan) password(nodeID int64, uuid string, protocol domain.Protocol, method string) string {
	if cp != nil {
		if pw, ok := cp.byNode[nodeID]; ok && pw != "" {
			return pw
		}
	}
	return crypto.DeriveProxyPassword(uuid, protocol, method)
}
