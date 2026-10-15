package nodesync

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	protocol "github.com/KazuhaHub/passwall-protocol/protocol"
)

const issuedRosterCacheEntries = 256
const issuedRosterCacheBytes = 32 << 20

// issuedRoster is private immutable syntax, never an observation or current
// desired state. Neither the map nor its client listener slices may escape.
type issuedRoster struct {
	minConfigVersion protocol.Version
	clients          map[protocol.ClientKey]protocol.Client
}

func (s *Service) decodeIssuedRoster(body []byte) (*issuedRoster, error) {
	// Hash the bytes read now: an ETag or version alone cannot detect a changed
	// or corrupt repository body. Every caller still checks current receipts,
	// epochs, freshness and desired identity independently.
	digest := sha256.Sum256(body)
	key := hex.EncodeToString(digest[:])
	if parsed, found := s.issuedRosterCache.Get(key); found {
		return parsed, nil
	}
	var roster protocol.RosterBody
	if err := json.Unmarshal(body, &roster); err != nil {
		return nil, err
	}
	parsed := &issuedRoster{
		minConfigVersion: roster.MinConfigVersion,
		clients:          make(map[protocol.ClientKey]protocol.Client, len(roster.Clients)),
	}
	// Account conservatively for map/struct/slice overhead and decoded strings
	// in addition to source bytes. Oversized bodies are decoded normally but
	// never retained. Duplicate keys retain the existing last-row-wins behavior.
	weight := int64(len(body))*2 + int64(len(roster.Clients))*512 + 256
	for _, client := range roster.Clients {
		parsed.clients[client.Key] = client
		weight += int64(len(client.Listeners)) * 32
	}
	if weight <= issuedRosterCacheBytes {
		s.issuedRosterCache.Put(key, parsed, int(weight))
	}
	return parsed, nil
}
