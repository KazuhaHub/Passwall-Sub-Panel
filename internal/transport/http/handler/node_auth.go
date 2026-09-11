package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"

	nodeprotocol "github.com/KazuhaHub/passwall-node/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// NodeCredentialLookup is the narrow read side needed at the public node
// boundary. PSP stores only SHA-256 digests; the bearer value never enters the
// database, logs, report body, or coordinator service.
type NodeCredentialLookup interface {
	GetByCredentialSHA256(context.Context, string) (*domain.NodeAgent, error)
}

// NodeBearerAuthenticator binds one opaque bearer credential to the stable
// agent identity stored by PSP. Every parse or lookup failure intentionally
// collapses to the same error so this public endpoint is not an identity
// oracle.
type NodeBearerAuthenticator struct {
	agents NodeCredentialLookup
}

func NewNodeBearerAuthenticator(agents NodeCredentialLookup) (*NodeBearerAuthenticator, error) {
	if agents == nil {
		return nil, errors.New("node credential lookup is required")
	}
	return &NodeBearerAuthenticator{agents: agents}, nil
}

func (a *NodeBearerAuthenticator) Authenticate(request *http.Request) (string, error) {
	if request == nil {
		return "", ErrNodeAuthentication
	}
	values := request.Header.Values("Authorization")
	if len(values) != 1 {
		return "", ErrNodeAuthentication
	}
	scheme, credential, ok := strings.Cut(values[0], " ")
	if !ok || scheme != "Bearer" || !validNodeCredential(credential) {
		return "", ErrNodeAuthentication
	}
	digest := sha256.Sum256([]byte(credential))
	agent, err := a.agents.GetByCredentialSHA256(request.Context(), hex.EncodeToString(digest[:]))
	if err != nil || agent == nil || agent.AgentID == "" {
		return "", ErrNodeAuthentication
	}
	return agent.AgentID, nil
}

func validNodeCredential(value string) bool {
	if len(value) < nodeprotocol.MinNodeCredentialBytes || len(value) > nodeprotocol.MaxNodeCredentialBytes {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 0x21 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

var _ NodeAuthenticator = (*NodeBearerAuthenticator)(nil)
