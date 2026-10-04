package sqlstore

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/boundedcache"
)

// Only canonical policy bytes and the shape needed to check source metadata
// survive decoding. The complete listener config and decoded rule tree do not.
type policyConfigProof struct {
	policyBody            []byte
	etag, digest, collect string
	active, emptyAllowed  bool
}

func decodePolicyConfig(canonical []byte) (policyConfigProof, error) {
	var config protocol.ConfigBody
	if json.Unmarshal(canonical, &config) != nil || protocol.ValidateDestinationPolicy(config.Policy) != nil {
		return policyConfigProof{}, fmt.Errorf("%w: destination candidate config", domain.ErrValidation)
	}
	encoded, err := json.Marshal(config)
	if err != nil || !bytes.Equal(encoded, canonical) {
		return policyConfigProof{}, fmt.Errorf("%w: noncanonical destination candidate config", domain.ErrValidation)
	}
	policyBody, err := json.Marshal(config.Policy)
	if err != nil {
		return policyConfigProof{}, err
	}
	p := policyConfigProof{policyBody: policyBody, etag: contentETag(canonical), digest: protocol.PolicyDigest(config.Policy), emptyAllowed: config.Policy == nil}
	if config.Policy != nil {
		p.collect = string(config.Policy.Collect)
		p.active = len(config.Policy.Rules) > 0
		p.emptyAllowed = config.Policy.Collect == protocol.CollectHitsAndUsage && len(config.Policy.Exempt) == 0
	}
	return p, nil
}

func (r *nodeAgentRepo) policyConfig(canonical []byte) (policyConfigProof, error) {
	key := contentETag(canonical)
	// Same SHA-256 means the same previously validated canonical bytes. As in
	// MintStream's idle path, a cryptographic collision is not a realistic risk;
	// retaining/comparing another entire config would defeat the idle bound.
	r.policyConfigMu.Lock()
	defer r.policyConfigMu.Unlock()
	if r.policyConfigProofs == nil {
		r.policyConfigProofs = boundedcache.New[policyConfigProof](64, 32<<20)
	}
	if proof, found := r.policyConfigProofs.Get(key); found {
		return proof, nil
	}
	decode := r.policyConfigDecode
	if decode == nil {
		decode = decodePolicyConfig
	}
	proof, err := decode(canonical)
	if err != nil {
		return policyConfigProof{}, err
	}
	r.policyConfigProofs.Put(key, proof, len(proof.policyBody)+512)
	return proof, nil
}
