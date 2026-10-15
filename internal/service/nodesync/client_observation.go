package nodesync

import (
	protocol "github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// cloneClientIdentityReport serves only NativePanelClientSnapshot, whose result
// is ClientDetail. Presence of a counter and the exact roster object receipt
// are required for that identity, including a locally quota-closed client.
// Listener traffic, live IPs and other clients are not part of ClientDetail.
// The full panel facade retains its complete independent observation copy.
func cloneClientIdentityReport(in protocol.NodeReport, desired *ports.NativeDesiredSnapshot) protocol.NodeReport {
	wanted := make(map[protocol.ClientKey]struct{}, len(desired.Clients))
	for _, entry := range desired.Clients {
		if entry.Client != nil {
			wanted[protocol.NewClientKey(entry.Client.ID)] = struct{}{}
		}
	}
	out := protocol.NodeReport{
		AgentID: in.AgentID, ProtocolVersion: in.ProtocolVersion,
		ReportedAtMS: in.ReportedAtMS, Partial: in.Partial,
		AgentVersion: in.AgentVersion, CoreEngine: in.CoreEngine,
		CoreVersion: in.CoreVersion, CoreState: in.CoreState,
		Have: make(map[string]protocol.StreamState, len(in.Have)),
	}
	for stream, state := range in.Have {
		out.Have[stream] = state
	}
	for _, counter := range in.Clients {
		if _, found := wanted[counter.Key]; found {
			counter.LiveIPs = nil
			out.Clients = append(out.Clients, counter)
		}
	}
	for _, object := range in.Objects {
		if object.Stream == protocol.StreamRoster {
			if _, found := wanted[protocol.ClientKey(object.Key)]; found {
				out.Objects = append(out.Objects, object)
			}
		}
	}
	return out
}
