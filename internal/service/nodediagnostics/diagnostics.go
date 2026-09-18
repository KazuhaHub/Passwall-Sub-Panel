// Package nodediagnostics creates bounded, administrator-requested remote
// diagnostics. It records intent only: the collection happens on the
// authenticated, capability-gated task channel, and nothing here can widen what
// a node is willing to read.
package nodediagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-node/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// Kind is the task kind this service mints.
const Kind = nodeprotocol.TaskKindDiagnosticsCollectV1

type Request = nodeprotocol.DiagnosticsArgs
type Result = nodeprotocol.DiagnosticsResult

// Status is what the panel renders and what the API returns.
type Status struct {
	TaskID               string                     `json:"task_id"`
	AgentID              string                     `json:"agent_id"`
	Sections             []string                   `json:"sections"`
	MaxEvents            int                        `json:"max_events"`
	Status               domain.NodeAgentTaskStatus `json:"status"`
	NotAfterMS           int64                      `json:"not_after_ms"`
	DispatchClosed       bool                       `json:"dispatch_closed"`
	DispatchClosedReason string                     `json:"dispatch_closed_reason,omitempty"`
	CollectedAtMS        int64                      `json:"collected_at_ms,omitempty"`
	Recovered            bool                       `json:"recovered,omitempty"`
	Truncated            bool                       `json:"truncated,omitempty"`
	// Result is present only for a task that succeeded. It is the collected
	// diagnostic and nothing else: the section 13.3 exclusions are the node's
	// job, and this side does not re-open what it was given.
	Result      *Result    `json:"result,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
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
		return nil, errors.New("remote diagnostics require panels, agents, tasks, lifecycle settings and a task ID source")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Service{options: options}, nil
}

// capability is what a node advertises for this kind. The rule is the same one
// the upgrade path uses: absence means the node cannot do it, which is a
// different statement from a node that failed.
func capability() string { return nodeprotocol.TaskCapability(Kind) }

// Create records the intent to collect, or returns the collection already
// running for this agent.
//
// AT MOST ONE ACTIVE DIAGNOSTIC PER AGENT, and a repeat returns it rather than
// refusing. That is what "idempotently merged at the producer" asks for, and it
// means the returned status describes the TASK, not the request — a second
// caller asking for different sections is answered with the sections that are
// actually being collected, which is the honest answer to "what is happening".
//
// The merge is not atomic, so two simultaneous calls with different arguments
// can both mint. That window is inherent to merging at this layer rather than
// in the repository, and the consequence is one extra collection, not a wrong
// one.
func (s *Service) Create(ctx context.Context, panelID int64, request Request) (*Status, bool, error) {
	_, agent, err := s.owner(ctx, panelID)
	if err != nil {
		return nil, false, err
	}
	if !advertises(agent, capability()) {
		return nil, false, fmt.Errorf("%w: this node does not advertise %s", domain.ErrValidation, capability())
	}
	if err := nodeprotocol.ValidateDiagnosticsArgs(request); err != nil {
		return nil, false, fmt.Errorf("%w: %s", domain.ErrValidation, err.Error())
	}
	now := s.options.Now().UTC()
	if now.UnixMilli() <= 0 {
		return nil, false, fmt.Errorf("%w: diagnostics authorization time is invalid", domain.ErrValidation)
	}

	if active, err := s.options.Tasks.ActiveByKind(ctx, agent.AgentID, Kind); err != nil {
		return nil, false, err
	} else if active != nil {
		stored, err := s.status(active, agent)
		return stored, false, err
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
	// SECTION 13.1 BOUNDS THE DEADLINE ITSELF: a read-only collection has no
	// reason to stay authorized longer than a few minutes, and the window is
	// taken from the protocol constant rather than chosen here.
	lifecycle, err := domain.NewNodeTaskLifecycleSnapshot(
		now.UnixMilli(), now.Add(nodeprotocol.MaxDiagnosticsNotAfter).UnixMilli(), policy)
	if err != nil {
		return nil, false, err
	}
	id, err := s.options.IDs.Next() // Outside the repository's transaction; gaps are harmless.
	if err != nil {
		return nil, false, errors.New("cannot mint native diagnostic task identity")
	}
	args, err := json.Marshal(request)
	if err != nil {
		return nil, false, fmt.Errorf("%w: diagnostics request cannot be encoded", domain.ErrValidation)
	}
	task := &domain.NodeAgentTask{
		TaskID: id, AgentID: agent.AgentID, Kind: Kind, Args: args,
		InputSHA256: nodeprotocol.ComputeTaskInputSHA256(Kind, args),
		Status:      domain.NodeAgentTaskQueued, Lifecycle: lifecycle,
		// NO IDEMPOTENCY KEY, ON PURPOSE. A key merges equal input forever, so
		// the second diagnostic an operator runs with the same settings would be
		// answered with the first one's terminal result instead of collecting
		// again. The "one active" rule above is the merge this feature needs;
		// replay safety within one active window comes from it too.
	}
	stored, created, err := s.options.Tasks.CreateOrGet(ctx, task)
	if err != nil {
		return nil, false, err
	}
	status, err := s.status(stored, agent)
	return status, created, err
}

// Get returns one task the caller already knows about.
func (s *Service) Get(ctx context.Context, panelID int64, taskID string) (*Status, error) {
	_, agent, err := s.owner(ctx, panelID)
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
	return s.status(task, agent)
}

func (s *Service) owner(ctx context.Context, panelID int64) (*domain.XUIPanel, *domain.NodeAgent, error) {
	panel, err := s.options.Panels.GetByID(ctx, panelID)
	if err != nil {
		return nil, nil, err
	}
	if domain.NormalizePanelKind(panel.Kind) != domain.PanelKindPSP {
		return nil, nil, fmt.Errorf("%w: remote diagnostics are only available for PSP-native servers", domain.ErrValidation)
	}
	agent, err := s.options.Agents.GetByPanelID(ctx, panelID)
	if err != nil {
		return nil, nil, err
	}
	return panel, agent, nil
}

func (s *Service) status(task *domain.NodeAgentTask, agent *domain.NodeAgent) (*Status, error) {
	if task == nil {
		return nil, domain.ErrNotFound
	}
	var request Request
	if err := json.Unmarshal(task.Args, &request); err != nil {
		// A stored request the panel itself cannot read is corruption, not a
		// caller error: saying "not found" would send an operator hunting for a
		// task that is right there.
		return nil, fmt.Errorf("native diagnostic task %s has unreadable arguments", task.TaskID)
	}
	status := &Status{
		TaskID: task.TaskID, AgentID: task.AgentID,
		Sections: request.Sections, MaxEvents: request.MaxEvents,
		Status: task.Status, CompletedAt: task.CompletedAt,
	}
	if task.Lifecycle != nil {
		status.NotAfterMS = task.Lifecycle.NotAfterMS
	}
	if task.DispatchClosedAt != nil {
		status.DispatchClosed = true
		status.DispatchClosedReason = task.DispatchClosedReason
	}
	// THE RESULT IS PARSED, NOT TRUSTED. A node that reported success with a body
	// this panel cannot read has produced something a reader would mistake for a
	// diagnostic; saying so is better than rendering an empty one.
	if task.Status == domain.NodeAgentTaskSucceeded && len(task.Result) > 0 {
		var result Result
		if err := json.Unmarshal(task.Result, &result); err != nil {
			return nil, fmt.Errorf("native diagnostic task %s returned an unreadable result", task.TaskID)
		}
		status.Result = &result
		status.CollectedAtMS = result.CollectedAtMS
		status.Recovered = result.Recovered
		status.Truncated = result.Truncated
	}
	return status, nil
}

func advertises(agent *domain.NodeAgent, capability string) bool {
	for _, observed := range agent.ObservedCapabilities {
		if observed == capability {
			return true
		}
	}
	return false
}
