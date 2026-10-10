package nodesync

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"

	"github.com/KazuhaHub/passwall-protocol/protocol"
)

func (s *Service) encodePolicyConfig(body protocol.ConfigBody, compileKey string) ([]byte, error) {
	encode := s.policyConfigEncode
	if encode == nil {
		encode = func(body protocol.ConfigBody) ([]byte, error) { return json.Marshal(body) }
	}
	// An untracked compiler cannot promise that its policy is immutable.
	if compileKey == "" {
		return encode(body)
	}
	base := body
	base.Policy = nil
	baseBytes, err := json.Marshal(base)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(baseBytes)
	key := hex.EncodeToString(digest[:]) + ":" + compileKey
	if canonical, found := s.policyConfigCache.Get(key); found {
		// Repositories receive their own bytes: a failed or alternate adapter
		// cannot mutate a later node's cached configuration.
		return slices.Clone(canonical), nil
	}
	canonical, err := encode(body)
	if err != nil {
		return nil, err
	}
	s.policyConfigCache.Put(key, slices.Clone(canonical), len(canonical)+len(key)+128)
	return canonical, nil
}
