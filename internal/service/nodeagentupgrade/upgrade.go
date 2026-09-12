// Package nodeagentupgrade creates bounded, administrator-requested native
// agent upgrades. It records intent only: execution remains on the existing
// authenticated, capability-gated task channel.
package nodeagentupgrade

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-node/protocol"
	"golang.org/x/mod/semver"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// These additive DTOs intentionally do not depend on unpublished Node exports.
// Keep their wire shape aligned with the agent.upgrade.v1 handler when its
// official module revision is consumed. No arbitrary URL or command is input.
const Kind = "agent.upgrade.v1"

const (
	startAuthorization   = 10 * time.Minute
	observationFreshness = 2 * time.Minute
)

type Request struct {
	Version         string `json:"version"`
	ExpectedVersion string `json:"expected_version"`
}

type Result struct {
	Version         string `json:"version"`
	PreviousVersion string `json:"previous_version"`
	BinarySHA256    string `json:"binary_sha256"`
	Restarted       bool   `json:"restarted"`
}

type Status struct {
	TaskID               string                     `json:"task_id"`
	AgentID              string                     `json:"agent_id"`
	Version              string                     `json:"version"`
	ExpectedVersion      string                     `json:"expected_version"`
	Status               domain.NodeAgentTaskStatus `json:"status"`
	UpgradeState         string                     `json:"upgrade_state"`
	NotAfterMS           int64                      `json:"not_after_ms"`
	DispatchClosed       bool                       `json:"dispatch_closed"`
	DispatchClosedReason string                     `json:"dispatch_closed_reason,omitempty"`
	BinarySHA256         string                     `json:"binary_sha256,omitempty"`
	ObservedVersion      string                     `json:"observed_version,omitempty"`
	LastSeen             *time.Time                 `json:"last_seen,omitempty"`
	CompletedAt          *time.Time                 `json:"completed_at,omitempty"`
}

type IDSource interface{ Next() (string, error) }

type Options struct {
	Panels   ports.XUIPanelRepo
	Agents   ports.NodeAgentRepo
	Tasks    ports.NodeAgentTaskRepo
	Settings ports.SettingsRepo
	IDs      IDSource
	Now      func() time.Time
}

type Service struct{ options Options }

func New(options Options) (*Service, error) {
	if options.Panels == nil || options.Agents == nil || options.Tasks == nil || options.Settings == nil || options.IDs == nil {
		return nil, errors.New("native agent upgrades require panels, agents, tasks, lifecycle settings and a task ID source")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Service{options: options}, nil
}

var (
	releaseVersion   = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)
	requestKey       = regexp.MustCompile(`^[A-Za-z0-9_.:-]{16,128}$`)
	binaryDigest     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	observedIdentity = regexp.MustCompile(`^(v[^ ]+)(?: \([0-9a-f]{7,40}\))?$`)
)

func (r Request) Validate() error {
	if !canonicalVersion(r.Version) || !canonicalVersion(r.ExpectedVersion) {
		return fmt.Errorf("%w: exact canonical target and expected Node versions are required", domain.ErrValidation)
	}
	if semver.Compare(r.Version, r.ExpectedVersion) <= 0 {
		return fmt.Errorf("%w: native upgrade target must be newer than its expected current version", domain.ErrValidation)
	}
	return nil
}

func DecodeRequest(payload []byte) (Request, error) {
	var request Request
	if err := strictJSON(payload, &request); err != nil {
		return Request{}, fmt.Errorf("%w: invalid native upgrade request shape", domain.ErrValidation)
	}
	return request, request.Validate()
}

func canonicalVersion(value string) bool {
	if len(value) > 128 || !releaseVersion.MatchString(value) {
		return false
	}
	_, prerelease, present := strings.Cut(value, "-")
	if !present {
		return true
	}
	for _, part := range strings.Split(prerelease, ".") {
		if len(part) > 1 && part[0] == '0' && strings.Trim(part, "0123456789") == "" {
			return false
		}
	}
	return true
}

func (s *Service) Request(ctx context.Context, panelID int64, request Request, key string) (*Status, bool, error) {
	if err := request.Validate(); err != nil {
		return nil, false, err
	}
	if !requestKey.MatchString(key) {
		return nil, false, fmt.Errorf("%w: a 16..128 character request idempotency key is required", domain.ErrValidation)
	}
	panel, agent, err := s.owner(ctx, panelID)
	if err != nil {
		return nil, false, err
	}
	defaults := domain.DefaultNodeTaskLifecyclePolicy()
	settings, err := s.options.Settings.Load(ctx, ports.UISettings{
		NodeTaskOfflineReconcileDays: defaults.OfflineReconcileDays,
		NodeTaskBackupRestoreDays:    defaults.BackupRestoreDays,
		NodeTaskResultRetentionDays:  defaults.ResultRetentionDays,
	})
	if err != nil {
		return nil, false, errors.New("cannot load native task lifecycle policy")
	}
	policy := settings.NodeTaskLifecyclePolicy()
	if err := policy.Validate(); err != nil {
		return nil, false, err
	}
	now := s.options.Now().UTC()
	if now.UnixMilli() <= 0 || now.Add(startAuthorization).Before(now) {
		return nil, false, fmt.Errorf("%w: native upgrade authorization time is invalid", domain.ErrValidation)
	}
	lifecycle, err := domain.NewNodeTaskLifecycleSnapshot(now.UnixMilli(), now.Add(startAuthorization).UnixMilli(), policy)
	if err != nil {
		return nil, false, err
	}
	id, err := s.options.IDs.Next() // Outside the repository's transaction; gaps are harmless.
	if err != nil {
		return nil, false, errors.New("cannot mint native upgrade task identity")
	}
	args, _ := json.Marshal(request) // Two validated strings; encoding cannot fail.
	keySHA := sha256.Sum256([]byte(key))
	keyDigest := hex.EncodeToString(keySHA[:])
	task := &domain.NodeAgentTask{
		TaskID: id, AgentID: agent.AgentID, Kind: Kind, Args: args,
		InputSHA256: nodeprotocol.ComputeTaskInputSHA256(Kind, args),
		Status:      domain.NodeAgentTaskQueued, Lifecycle: lifecycle, IdempotencyKeySHA256: &keyDigest,
	}
	// The repository resolves equal idempotency input before admission. It
	// returns the ORIGINAL lifecycle, including after a restart or settings edit;
	// a request retry never extends permission or reopens a completed operation.
	stored, created, err := s.options.Tasks.CreateOrGet(ctx, task)
	if err != nil {
		return nil, false, err
	}
	if stored == nil || !bytes.Equal(stored.Args, args) || stored.IdempotencyKeySHA256 == nil || *stored.IdempotencyKeySHA256 != keyDigest {
		return nil, false, fmt.Errorf("%w: native upgrade repository returned a different request", domain.ErrConflict)
	}
	status, err := s.status(stored, panel, agent, now)
	return status, created, err
}

func (s *Service) Get(ctx context.Context, panelID int64, taskID string) (*Status, error) {
	panel, agent, err := s.owner(ctx, panelID)
	if err != nil {
		return nil, err
	}
	task, err := s.options.Tasks.GetByTaskID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if task == nil || task.TaskID != taskID || task.AgentID != agent.AgentID || task.Kind != Kind {
		return nil, domain.ErrNotFound
	}
	return s.status(task, panel, agent, s.options.Now().UTC())
}

func (s *Service) owner(ctx context.Context, panelID int64) (*domain.XUIPanel, *domain.NodeAgent, error) {
	if panelID <= 0 {
		return nil, nil, domain.ErrValidation
	}
	panel, err := s.options.Panels.GetByID(ctx, panelID)
	if err != nil {
		return nil, nil, err
	}
	if panel == nil || panel.ID != panelID || domain.NormalizePanelKind(panel.Kind) != domain.PanelKindPSP {
		return nil, nil, fmt.Errorf("%w: native agent upgrades require a PSP native server", domain.ErrValidation)
	}
	agent, err := s.options.Agents.GetByPanelID(ctx, panelID)
	if err != nil {
		return nil, nil, err
	}
	if agent == nil || agent.AgentID == "" || agent.PanelID != panelID {
		return nil, nil, domain.ErrConflict
	}
	return panel, agent, nil
}

func (s *Service) status(task *domain.NodeAgentTask, panel *domain.XUIPanel, agent *domain.NodeAgent, now time.Time) (*Status, error) {
	if task == nil || task.Kind != Kind || task.AgentID != agent.AgentID || !task.Status.Valid() ||
		task.Lifecycle == nil || task.Lifecycle.Validate() != nil ||
		task.InputSHA256 != nodeprotocol.ComputeTaskInputSHA256(Kind, task.Args) {
		return nil, fmt.Errorf("%w: native upgrade task stored identity is invalid", domain.ErrConflict)
	}
	if nodeprotocol.ValidateTasks([]nodeprotocol.Task{{ID: task.TaskID, Kind: Kind, Args: task.Args, InputSHA256: task.InputSHA256, NotAfterMS: task.Lifecycle.NotAfterMS}}) != nil {
		return nil, fmt.Errorf("%w: native upgrade task wire identity is invalid", domain.ErrConflict)
	}
	var request Request
	if strictJSON(task.Args, &request) != nil || request.Validate() != nil {
		return nil, fmt.Errorf("%w: native upgrade task stored input is invalid", domain.ErrConflict)
	}
	canonicalArgs, _ := json.Marshal(request)
	if !bytes.Equal(task.Args, canonicalArgs) {
		return nil, fmt.Errorf("%w: native upgrade input is not canonical", domain.ErrConflict)
	}
	status := &Status{
		TaskID: task.TaskID, AgentID: agent.AgentID, Version: request.Version, ExpectedVersion: request.ExpectedVersion,
		Status: task.Status, NotAfterMS: task.Lifecycle.NotAfterMS, DispatchClosed: task.DispatchClosedAt != nil,
		LastSeen: cloneTime(agent.LastSeen), CompletedAt: cloneTime(task.CompletedAt),
	}
	if task.DispatchClosedAt != nil {
		switch task.DispatchClosedReason {
		case "task_authorization_expired", "unknown_task", "never_offered":
			status.DispatchClosedReason = task.DispatchClosedReason
		default:
			status.DispatchClosedReason = "dispatch_closed"
		}
	}
	if identity := observedIdentity.FindStringSubmatch(panel.PanelVersion); len(identity) == 2 && canonicalVersion(identity[1]) {
		status.ObservedVersion = identity[1]
	}
	switch task.Status {
	case domain.NodeAgentTaskQueued:
		status.UpgradeState = "queued"
	case domain.NodeAgentTaskOffered:
		status.UpgradeState = "offered"
	case domain.NodeAgentTaskFailed:
		status.UpgradeState = "failed"
	case domain.NodeAgentTaskIndeterminate:
		status.UpgradeState = "manual_attention"
	case domain.NodeAgentTaskSucceeded:
		var result Result
		if task.ResultOK == nil || !*task.ResultOK || task.ResultIndeterminate || task.CompletedAt == nil ||
			strictJSON(task.Result, &result) != nil || !result.Restarted || !binaryDigest.MatchString(result.BinarySHA256) ||
			result.Version != request.Version || result.PreviousVersion != request.ExpectedVersion {
			status.UpgradeState = "manual_attention"
			break
		}
		status.BinarySHA256 = result.BinarySHA256
		status.UpgradeState = "awaiting_observation"
		// PanelVersion is the REAL durable report.AgentVersion observation written
		// by nodesync. There is no ReportJSON/ObservedAgentVersion field on NodeAgent.
		// A durable receipt or generic success alone does not prove restart health.
		if status.ObservedVersion == request.Version && freshAt(agent.LastSeen, task.CompletedAt, now) && freshAt(panel.VersionCheckedAt, task.CompletedAt, now) {
			status.UpgradeState = "verified"
		}
	}
	if !task.Status.Terminal() && task.DispatchClosedAt != nil {
		status.UpgradeState = "dispatch_closed"
	}
	return status, nil
}

func strictJSON(payload []byte, target any) error {
	var allowed map[string]bool
	switch target.(type) {
	case *Request:
		allowed = map[string]bool{"version": true, "expected_version": true}
	case *Result:
		allowed = map[string]bool{"version": true, "previous_version": true, "binary_sha256": true, "restarted": true}
	default:
		return errors.New("invalid upgrade metadata target")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return errors.New("invalid structured upgrade metadata")
	}
	seen := make(map[string]bool, len(allowed))
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || !allowed[key] || seen[key] {
			return errors.New("invalid upgrade metadata fields")
		}
		seen[key] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return errors.New("invalid upgrade metadata value")
		}
	}
	if closing, err := decoder.Token(); err != nil || closing != json.Delim('}') || len(seen) != len(allowed) {
		return errors.New("invalid upgrade metadata fields")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("invalid trailing upgrade metadata")
	}
	if err := json.Unmarshal(payload, target); err != nil {
		return errors.New("invalid upgrade metadata types")
	}
	return nil
}

func freshAt(seen, completed *time.Time, now time.Time) bool {
	return seen != nil && completed != nil && !seen.Before(*completed) && !seen.After(now) && now.Sub(*seen) <= observationFreshness
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
