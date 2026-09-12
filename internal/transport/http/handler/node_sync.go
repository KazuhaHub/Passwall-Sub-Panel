package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	nodeprotocol "github.com/KazuhaHub/passwall-node/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
)

var ErrNodeAuthentication = errors.New("node authentication failed")

type NodeSyncService interface {
	Sync(context.Context, nodeprotocol.NodeReport) (nodeprotocol.SyncResponse, error)
}

// NodeAuthenticator keeps credential verification outside the stable C1/C2
// protocol body and coordinator. Production uses NodeBearerAuthenticator;
// tests may inject a focused function implementation.
type NodeAuthenticator interface {
	Authenticate(*http.Request) (agentID string, err error)
}

type NodeAuthenticatorFunc func(*http.Request) (string, error)

func (f NodeAuthenticatorFunc) Authenticate(request *http.Request) (string, error) {
	return f(request)
}

type NodeSyncHandler struct {
	service NodeSyncService
	auth    NodeAuthenticator
}

func NewNodeSyncHandler(service NodeSyncService, auth NodeAuthenticator) (*NodeSyncHandler, error) {
	if service == nil || auth == nil {
		return nil, errors.New("node sync service and authenticator are required")
	}
	return &NodeSyncHandler{service: service, auth: auth}, nil
}

func (h *NodeSyncHandler) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || request.URL.Path != "/v1/node/sync" {
		http.NotFound(w, request)
		return
	}
	agentID, err := h.auth.Authenticate(request)
	if err != nil || agentID == "" {
		writeNodeError(w, http.StatusUnauthorized, ErrNodeAuthentication)
		return
	}
	limited := http.MaxBytesReader(w, request.Body, nodeprotocol.MaxSyncBodyBytes)
	defer limited.Close()
	decoder := json.NewDecoder(limited)
	var report nodeprotocol.NodeReport
	if err := decoder.Decode(&report); err != nil {
		status := http.StatusBadRequest
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		writeNodeError(w, status, fmt.Errorf("decode node report: %w", err))
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = errors.New("trailing JSON value")
		}
		writeNodeError(w, http.StatusBadRequest, fmt.Errorf("decode node report: %w", err))
		return
	}
	if report.AgentID != agentID {
		writeNodeError(w, http.StatusUnauthorized, ErrNodeAuthentication)
		return
	}
	if err := nodeprotocol.ValidateNodeReport(report); err != nil {
		writeNodeError(w, http.StatusBadRequest, fmt.Errorf("validate node report: %w", err))
		return
	}
	response, err := h.service.Sync(request.Context(), report)
	if err != nil {
		// The untrusted shape was already validated above. Everything after this
		// boundary must leave the node's immutable outbox retryable. In particular,
		// evidence capacity cannot be acknowledged until durable receipt succeeds.
		// Never echo repository detail or untrusted result content to the caller.
		log.Error("native node sync failed", "agent_id", agentID, "err", err)
		status := http.StatusInternalServerError
		if errors.Is(err, domain.ErrResourceExhausted) {
			status = http.StatusTooManyRequests
		}
		writeNodeError(w, status, errors.New("node sync failed"))
		return
	}
	payload, err := json.Marshal(response)
	if err != nil || int64(len(payload)) > nodeprotocol.MaxSyncBodyBytes {
		log.Error("native node sync response encoding failed", "agent_id", agentID, "bytes", len(payload), "err", err)
		writeNodeError(w, http.StatusInternalServerError, errors.New("node sync failed"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}

func writeNodeError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

var _ http.Handler = (*NodeSyncHandler)(nil)
