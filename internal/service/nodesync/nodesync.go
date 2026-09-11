// Package nodesync owns PSP's side of the native node protocol. It is the one
// minter for config, roster and directives, and the one ingestion point for
// agent observations. adapters/pspnode projects its latest full report into
// the legacy PanelClient port; it never invents observed state from intent.
package nodesync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/KazuhaHub/passwall-node/corecatalog"
	nodeprotocol "github.com/KazuhaHub/passwall-node/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

const (
	defaultNextPollSeconds = 30
	defaultFullReportSecs  = 60
)

type Service struct {
	desired  ports.NativeDesiredSnapshotRepo
	agents   ports.NodeAgentRepo
	issues   ports.NodeAgentIssueRepo
	users    ports.UserRepo
	clients  ports.PSPClientRepo
	nodes    ports.NodeRepo
	settings ports.SettingsReader
	panels   ports.XUIPanelRepo

	mu      sync.RWMutex
	reports map[string]receivedFullReport
	anchors map[int64]nodeprotocol.ClientCounters
	grants  map[string]map[nodeprotocol.ClientKey]int64
	now     func() time.Time
	// invalidateRender is late-bound because the native adapter needs this
	// service before the panel pool (and therefore render service) can exist.
	invalidateRender func()
}

// receivedFullReport keeps the control plane's receipt time beside the latest
// full enumeration. ReportedAtMS is useful for clock-skew diagnostics, but it
// is agent-controlled and therefore must never decide freshness, coverage, or
// an authorization bound.
type receivedFullReport struct {
	report       nodeprotocol.NodeReport
	receivedAtMS int64
}

type Options struct {
	Desired  ports.NativeDesiredSnapshotRepo
	Agents   ports.NodeAgentRepo
	Issues   ports.NodeAgentIssueRepo
	Users    ports.UserRepo
	Clients  ports.PSPClientRepo
	Nodes    ports.NodeRepo
	Settings ports.SettingsReader
	Panels   ports.XUIPanelRepo
	Now      func() time.Time
}

func New(options Options) (*Service, error) {
	if options.Desired == nil || options.Agents == nil || options.Issues == nil || options.Users == nil ||
		options.Clients == nil || options.Nodes == nil || options.Settings == nil {
		return nil, errors.New("nodesync: desired, agents, issues, users, clients, nodes and settings are required")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Service{
		desired: options.Desired, agents: options.Agents, issues: options.Issues, users: options.Users,
		clients: options.Clients, nodes: options.Nodes, settings: options.Settings,
		panels:  options.Panels,
		reports: make(map[string]receivedFullReport),
		anchors: make(map[int64]nodeprotocol.ClientCounters), now: now,
		grants: make(map[string]map[nodeprotocol.ClientKey]int64),
	}, nil
}

func (s *Service) SetRenderInvalidator(invalidate func()) { s.invalidateRender = invalidate }

// Sync ingests one agent observation and returns all three independently
// conditional segments in the same round trip. Authentication stays outside
// this service: the production HTTP boundary resolves a strict Bearer digest
// to the same agent ID before calling this coordinator.
func (s *Service) Sync(ctx context.Context, report nodeprotocol.NodeReport) (nodeprotocol.SyncResponse, error) {
	if err := nodeprotocol.ValidateNodeReport(report); err != nil {
		return nodeprotocol.SyncResponse{}, fmt.Errorf("nodesync: invalid report: %w", err)
	}
	agent, err := s.agents.GetByAgentID(ctx, report.AgentID)
	if err != nil {
		return nodeprotocol.SyncResponse{}, fmt.Errorf("nodesync: resolve agent: %w", err)
	}
	snapshot, err := s.desired.Load(ctx, agent.PanelID)
	if err != nil {
		return nodeprotocol.SyncResponse{}, fmt.Errorf("nodesync: desired snapshot: %w", err)
	}
	now := s.now().UTC()
	if err := s.ingestReport(ctx, agent, snapshot, report, now); err != nil {
		return nodeprotocol.SyncResponse{}, err
	}
	if err := s.recordPanelObservation(ctx, agent, report, now); err != nil {
		return nodeprotocol.SyncResponse{}, err
	}

	configBody, err := buildConfig(snapshot, agent)
	if err != nil {
		return nodeprotocol.SyncResponse{}, err
	}
	configStream, err := s.mint(ctx, agent, domain.NodeAgentStreamConfig, configBody, now)
	if err != nil {
		return nodeprotocol.SyncResponse{}, err
	}
	rosterBody := buildRoster(snapshot, nodeprotocol.Version{Epoch: agent.Epoch, Version: configStream.DesiredVersion})
	rosterStream, err := s.mint(ctx, agent, domain.NodeAgentStreamRoster, rosterBody, now)
	if err != nil {
		return nodeprotocol.SyncResponse{}, err
	}
	directivesBody, envelope, err := s.buildDirectives(ctx, agent, snapshot, report,
		nodeprotocol.Version{Epoch: agent.Epoch, Version: rosterStream.DesiredVersion}, now)
	if err != nil {
		return nodeprotocol.SyncResponse{}, err
	}
	directivesStream, err := s.mint(ctx, agent, domain.NodeAgentStreamDirectives, directivesBody, now)
	if err != nil {
		return nodeprotocol.SyncResponse{}, err
	}

	return nodeprotocol.SyncResponse{
		Envelope:   envelope,
		Config:     segmentFor(report.Have[nodeprotocol.StreamConfig], agent.Epoch, configStream, configBody),
		Roster:     segmentFor(report.Have[nodeprotocol.StreamRoster], agent.Epoch, rosterStream, rosterBody),
		Directives: segmentFor(report.Have[nodeprotocol.StreamDirectives], agent.Epoch, directivesStream, directivesBody),
	}, nil
}

func (s *Service) recordPanelObservation(ctx context.Context, agent *domain.NodeAgent, report nodeprotocol.NodeReport, now time.Time) error {
	if s.panels == nil || agent == nil {
		return nil
	}
	panel, err := s.panels.GetByID(ctx, agent.PanelID)
	if err != nil {
		return fmt.Errorf("nodesync: load native panel observation target: %w", err)
	}
	panelVersion := report.AgentVersion
	if panelVersion == "" {
		panelVersion = panel.PanelVersion
	}
	coreVersion := report.CoreVersion
	if coreVersion == "" {
		coreVersion = panel.XrayVersion
	}
	if err := s.panels.UpdateVersion(ctx, agent.PanelID, panelVersion, coreVersion, &now); err != nil {
		return fmt.Errorf("nodesync: persist native panel observation: %w", err)
	}
	if coreVersion != panel.XrayVersion && s.invalidateRender != nil {
		s.invalidateRender()
	}
	return nil
}

func (s *Service) mint(ctx context.Context, agent *domain.NodeAgent, stream domain.NodeAgentStreamName, body any, now time.Time) (*domain.NodeAgentStream, error) {
	canonical, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("nodesync: encode %s: %w", stream, err)
	}
	stored, _, err := s.agents.MintStream(ctx, agent.AgentID, stream, canonical, now)
	if err != nil {
		return nil, fmt.Errorf("nodesync: mint %s: %w", stream, err)
	}
	return stored, nil
}

type listenerConfig struct {
	Enabled        bool   `json:"enabled"`
	Listen         string `json:"listen"`
	Port           int    `json:"port"`
	Protocol       string `json:"protocol"`
	Remark         string `json:"remark"`
	Settings       string `json:"settings"`
	StreamSettings string `json:"stream_settings"`
	Sniffing       string `json:"sniffing"`
	Allocate       string `json:"allocate"`
	ExpiryTime     int64  `json:"expiry_time"`
}

func buildConfig(snapshot *ports.NativeDesiredSnapshot, agent *domain.NodeAgent) (nodeprotocol.ConfigBody, error) {
	version := ""
	allowRestricted := false
	if agent != nil {
		version = agent.DesiredCoreVersion
		allowRestricted = agent.AllowRestrictedReality
	}
	var release corecatalog.Release
	var err error
	if version == "" {
		release, err = corecatalog.Recommended("xray")
	} else {
		release, err = corecatalog.Resolve("xray", version)
	}
	if err != nil {
		return nodeprotocol.ConfigBody{}, fmt.Errorf("nodesync: resolve desired core: %w", err)
	}
	if release.RequiresConfirmation != allowRestricted {
		return nodeprotocol.ConfigBody{}, fmt.Errorf("nodesync: desired core %s restriction acknowledgement mismatch", release.Version)
	}
	body := nodeprotocol.ConfigBody{
		Listeners: make([]nodeprotocol.Listener, 0, len(snapshot.Nodes)),
		Core: nodeprotocol.CoreSelection{
			Engine: "xray", Version: release.Version, AllowRestrictedReality: allowRestricted,
		},
	}
	for _, node := range snapshot.Nodes {
		if node == nil {
			continue
		}
		raw, err := json.Marshal(listenerConfig{
			Enabled: node.Enabled, Listen: node.InboundListen, Port: node.DesiredPort,
			Protocol: node.DesiredProtocol, Remark: node.InboundRemark,
			Settings: node.InboundSettings, StreamSettings: node.StreamSettings,
			Sniffing: node.Sniffing, Allocate: node.Allocate, ExpiryTime: node.InboundExpiryTime,
		})
		if err != nil {
			return nodeprotocol.ConfigBody{}, fmt.Errorf("nodesync: encode listener %d: %w", node.ID, err)
		}
		body.Listeners = append(body.Listeners, nodeprotocol.Listener{
			Key: nodeprotocol.NewListenerKey(node.ID), Config: nodeprotocol.RawConfig(raw),
		})
	}
	body.Coverage = nodeprotocol.SegmentCounts{Entries: len(body.Listeners)}
	return body, nil
}

func buildRoster(snapshot *ports.NativeDesiredSnapshot, configVersion nodeprotocol.Version) nodeprotocol.RosterBody {
	nodeIDs := make(map[int64]struct{}, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		if node != nil {
			nodeIDs[node.ID] = struct{}{}
		}
	}
	body := nodeprotocol.RosterBody{
		Clients:          make([]nodeprotocol.Client, 0, len(snapshot.Clients)),
		MinConfigVersion: configVersion,
	}
	subjects := make(map[int64]struct{})
	for _, desired := range snapshot.Clients {
		client := desired.Client
		if client == nil {
			continue
		}
		listeners := make([]nodeprotocol.ListenerKey, 0, len(desired.Inbounds))
		flow := ""
		for _, attachment := range desired.Inbounds {
			if _, exists := nodeIDs[attachment.NodeID]; exists {
				listeners = append(listeners, nodeprotocol.NewListenerKey(attachment.NodeID))
				if attachment.FlowOverride != "" {
					if flow != "" && flow != attachment.FlowOverride {
						// clientplan guarantees one flow class per client row. Keep
						// this defensive branch deterministic; the node compiler will
						// reject a malformed plan instead of receiving an arbitrary
						// last attachment's value.
						flow = "__conflict__"
					} else {
						flow = attachment.FlowOverride
					}
				}
			}
		}
		sort.Slice(listeners, func(i, j int) bool { return listeners[i] < listeners[j] })
		body.Clients = append(body.Clients, nodeprotocol.Client{
			Key: nodeprotocol.NewClientKey(client.ID), Subject: nodeprotocol.NewSubjectKey(client.UserID),
			Listeners: listeners, Enabled: client.DesiredEnable,
			ExpiresAtMS: client.DesiredExpiryTime,
			Credentials: nodeprotocol.Credential{
				Username: client.Email, UUID: client.UUID, Password: client.Password, Flow: flow,
			},
		})
		subjects[client.UserID] = struct{}{}
	}
	body.Coverage = nodeprotocol.SegmentCounts{Entries: len(body.Clients), Subjects: len(subjects)}
	return body
}

func segmentFor[T any](have nodeprotocol.StreamState, epoch uint64, stream *domain.NodeAgentStream, body T) nodeprotocol.Segment[T] {
	result := nodeprotocol.Segment[T]{
		Version: nodeprotocol.Version{Epoch: epoch, Version: stream.DesiredVersion},
		ETag:    nodeprotocol.ETag(stream.DesiredETag),
	}
	if have.Applied.Epoch == epoch && string(have.ETag) == stream.DesiredETag && stream.DesiredETag != "" {
		result.Unchanged = true
		return result
	}
	result.Body = &body
	return result
}

func (s *Service) ingestReport(ctx context.Context, agent *domain.NodeAgent, snapshot *ports.NativeDesiredSnapshot, report nodeprotocol.NodeReport, now time.Time) error {
	if len(report.TaskResults) != 0 {
		// §9 has not defined task kinds or their durable PSP-side state yet. Do
		// not acknowledge and discard an executed side effect: a non-2xx sync
		// keeps the result in the agent's transactional outbox for a future
		// implementation to consume.
		return errors.New("nodesync: task result ingestion is not configured")
	}
	issues := make([]domain.NodeAgentIssue, len(report.Issues))
	for i := range report.Issues {
		issues[i] = domain.NodeAgentIssue{
			AgentID: agent.AgentID, Code: report.Issues[i].Code,
			Key: report.Issues[i].Key, Detail: report.Issues[i].Detail,
		}
	}
	if err := s.issues.RecordBatch(ctx, agent.AgentID, issues, now); err != nil {
		return fmt.Errorf("nodesync: record agent issues: %w", err)
	}
	streams := make(map[string]*domain.NodeAgentStream, 3)
	for _, item := range []struct {
		wire string
		db   domain.NodeAgentStreamName
	}{
		{nodeprotocol.StreamConfig, domain.NodeAgentStreamConfig},
		{nodeprotocol.StreamRoster, domain.NodeAgentStreamRoster},
		{nodeprotocol.StreamDirectives, domain.NodeAgentStreamDirectives},
	} {
		stream, err := s.agents.GetStream(ctx, agent.AgentID, item.db)
		if err != nil {
			return fmt.Errorf("nodesync: load %s state: %w", item.wire, err)
		}
		streams[item.wire] = stream
		have := report.Have[item.wire]
		if err := s.agents.RecordApplied(ctx, agent.AgentID, item.db,
			have.Applied.Epoch, have.Applied.Version, string(have.ETag), now); err != nil {
			return fmt.Errorf("nodesync: record %s applied: %w", item.wire, err)
		}
	}
	if report.Partial {
		return nil
	}
	s.mu.Lock()
	s.reports[agent.AgentID] = receivedFullReport{
		report: cloneReport(report), receivedAtMS: now.UnixMilli(),
	}
	s.mu.Unlock()
	return s.reconcileObjects(ctx, agent, snapshot, report, streams, now)
}

func (s *Service) reconcileObjects(ctx context.Context, agent *domain.NodeAgent, snapshot *ports.NativeDesiredSnapshot, report nodeprotocol.NodeReport, streams map[string]*domain.NodeAgentStream, now time.Time) error {
	missing := make([]domain.NodeAgentIssue, 0)
	rosterHave := report.Have[nodeprotocol.StreamRoster]
	rosterCurrent := streams[nodeprotocol.StreamRoster]
	if rosterCurrent != nil && rosterHave.Applied.Epoch == agent.Epoch && string(rosterHave.ETag) == rosterCurrent.DesiredETag {
		var appliedRoster nodeprotocol.RosterBody
		if err := json.Unmarshal(rosterCurrent.DesiredBody, &appliedRoster); err != nil {
			return fmt.Errorf("nodesync: decode applied roster: %w", err)
		}
		appliedClients := make(map[nodeprotocol.ClientKey]nodeprotocol.Client, len(appliedRoster.Clients))
		for _, client := range appliedRoster.Clients {
			appliedClients[client.Key] = client
		}
		currentRoster := buildRoster(snapshot, appliedRoster.MinConfigVersion)
		statuses := objectStatuses(report.Objects, nodeprotocol.StreamRoster)
		for _, currentClient := range currentRoster.Clients {
			appliedClient, existed := appliedClients[currentClient.Key]
			if !existed || !sameProtocolClient(appliedClient, currentClient) {
				continue
			}
			clientID, parseErr := currentClient.Key.RowID()
			if parseErr != nil {
				continue
			}
			status, exists := statuses[string(currentClient.Key)]
			state := domain.ClientApplyPending
			var failedAt *time.Time
			if exists {
				state = clientApplyState(status.State)
				if status.FirstFailedAtMS > 0 {
					v := time.UnixMilli(status.FirstFailedAtMS).UTC()
					failedAt = &v
				}
			} else {
				missing = append(missing, domain.NodeAgentIssue{
					AgentID: agent.AgentID, Code: nodeprotocol.IssueReportMissingObject,
					Key:    string(currentClient.Key),
					Detail: "roster object missing from full report",
				})
			}
			var desired *ports.NativeDesiredClient
			for i := range snapshot.Clients {
				if snapshot.Clients[i].Client != nil && snapshot.Clients[i].Client.ID == clientID {
					desired = &snapshot.Clients[i]
					break
				}
			}
			if desired == nil {
				continue
			}
			for _, attachment := range desired.Inbounds {
				update := domain.PSPClientInbound{
					ClientID: clientID, NodeID: attachment.NodeID,
					State: state, AppliedVersion: rosterHave.Applied.Version, FirstFailedAt: failedAt,
				}
				if state == domain.ClientApplyApplied {
					update.AppliedEmail = appliedClient.Credentials.Username
					update.AppliedUUID = appliedClient.Credentials.UUID
					update.AppliedPassword = appliedClient.Credentials.Password
				}
				if err := s.clients.UpdateInboundState(ctx, update); err != nil {
					return fmt.Errorf("nodesync: update client %d attachment %d: %w", clientID, attachment.NodeID, err)
				}
			}
		}
	}

	configHave := report.Have[nodeprotocol.StreamConfig]
	configCurrent := streams[nodeprotocol.StreamConfig]
	if configCurrent != nil && configHave.Applied.Epoch == agent.Epoch && string(configHave.ETag) == configCurrent.DesiredETag {
		var appliedConfig nodeprotocol.ConfigBody
		if err := json.Unmarshal(configCurrent.DesiredBody, &appliedConfig); err != nil {
			return fmt.Errorf("nodesync: decode applied config: %w", err)
		}
		appliedListeners := make(map[nodeprotocol.ListenerKey]listenerConfig, len(appliedConfig.Listeners))
		for _, listener := range appliedConfig.Listeners {
			var config listenerConfig
			if err := json.Unmarshal(listener.Config, &config); err != nil {
				return fmt.Errorf("nodesync: decode applied listener %s: %w", listener.Key, err)
			}
			appliedListeners[listener.Key] = config
		}
		statuses := objectStatuses(report.Objects, nodeprotocol.StreamConfig)
		for _, node := range snapshot.Nodes {
			if node == nil {
				continue
			}
			status, exists := statuses[string(nodeprotocol.NewListenerKey(node.ID))]
			applied, existed := appliedListeners[nodeprotocol.NewListenerKey(node.ID)]
			if existed && !exists {
				missing = append(missing, domain.NodeAgentIssue{
					AgentID: agent.AgentID, Code: nodeprotocol.IssueReportMissingObject,
					Key:    string(nodeprotocol.NewListenerKey(node.ID)),
					Detail: "config object missing from full report",
				})
			}
			if !exists || !existed || status.State != nodeprotocol.ObjectApplied || !sameListenerIntent(applied, node) {
				continue
			}
			if err := s.nodes.UpdateObservedEndpoint(ctx, node.ID, domain.NodeObservedEndpoint{
				Protocol: node.DesiredProtocol, Port: node.DesiredPort,
			}); err != nil {
				return fmt.Errorf("nodesync: update listener %d observed endpoint: %w", node.ID, err)
			}
		}
	}
	if err := s.issues.RecordBatch(ctx, agent.AgentID, missing, now); err != nil {
		return fmt.Errorf("nodesync: record missing report objects: %w", err)
	}
	return nil
}

func sameProtocolClient(a, b nodeprotocol.Client) bool {
	if a.Key != b.Key || a.Subject != b.Subject || a.Enabled != b.Enabled ||
		a.ExpiresAtMS != b.ExpiresAtMS || a.Credentials != b.Credentials || len(a.Listeners) != len(b.Listeners) {
		return false
	}
	for i := range a.Listeners {
		if a.Listeners[i] != b.Listeners[i] {
			return false
		}
	}
	return true
}

func sameListenerIntent(applied listenerConfig, node *domain.Node) bool {
	return node != nil && applied.Enabled == node.Enabled && applied.Listen == node.InboundListen &&
		applied.Port == node.DesiredPort && applied.Protocol == node.DesiredProtocol &&
		applied.Remark == node.InboundRemark && applied.Settings == node.InboundSettings &&
		applied.StreamSettings == node.StreamSettings && applied.Sniffing == node.Sniffing &&
		applied.Allocate == node.Allocate && applied.ExpiryTime == node.InboundExpiryTime
}

func objectStatuses(all []nodeprotocol.ObjectStatus, stream string) map[string]nodeprotocol.ObjectStatus {
	out := make(map[string]nodeprotocol.ObjectStatus)
	for _, status := range all {
		if status.Stream == stream {
			out[status.Key] = status
		}
	}
	return out
}

func clientApplyState(state nodeprotocol.ObjectState) domain.ClientApplyState {
	switch state {
	case nodeprotocol.ObjectApplied:
		return domain.ClientApplyApplied
	case nodeprotocol.ObjectRejected:
		return domain.ClientApplyRejected
	case nodeprotocol.ObjectBlocked:
		return domain.ClientApplyBlocked
	default:
		return domain.ClientApplyPending
	}
}

func cloneReport(in nodeprotocol.NodeReport) nodeprotocol.NodeReport {
	out := in
	out.Have = make(map[string]nodeprotocol.StreamState, len(in.Have))
	for key, value := range in.Have {
		out.Have[key] = value
	}
	out.Objects = append([]nodeprotocol.ObjectStatus(nil), in.Objects...)
	out.ListenerCounters = append([]nodeprotocol.ListenerCounters(nil), in.ListenerCounters...)
	out.Clients = append([]nodeprotocol.ClientCounters(nil), in.Clients...)
	for i := range out.Clients {
		out.Clients[i].LiveIPs = append([]string(nil), in.Clients[i].LiveIPs...)
	}
	out.Subjects = append([]nodeprotocol.SubjectObservation(nil), in.Subjects...)
	out.Issues = append([]nodeprotocol.Issue(nil), in.Issues...)
	out.TaskResults = append([]nodeprotocol.TaskResult(nil), in.TaskResults...)
	return out
}

func (s *Service) buildDirectives(ctx context.Context, agent *domain.NodeAgent, snapshot *ports.NativeDesiredSnapshot, current nodeprotocol.NodeReport, rosterVersion nodeprotocol.Version, now time.Time) (nodeprotocol.DirectivesBody, nodeprotocol.Envelope, error) {
	settings, err := s.settings.Load(ctx, ports.UISettings{
		NodePollSeconds: defaultNextPollSeconds, FullReportSeconds: defaultFullReportSecs,
	})
	if err != nil {
		return nodeprotocol.DirectivesBody{}, nodeprotocol.Envelope{}, fmt.Errorf("nodesync: load settings: %w", err)
	}
	periodNow := now
	if settings.Timezone != "" {
		loc, loadErr := time.LoadLocation(settings.Timezone)
		if loadErr != nil {
			return nodeprotocol.DirectivesBody{}, nodeprotocol.Envelope{}, fmt.Errorf("nodesync: load timezone %q: %w", settings.Timezone, loadErr)
		}
		periodNow = now.In(loc)
	}
	nextPollSeconds := settings.NodePollSeconds
	if nextPollSeconds <= 0 {
		nextPollSeconds = defaultNextPollSeconds
	}
	fullFreshnessSeconds := effectiveFullReportPeriod(settings.FullReportSeconds, nextPollSeconds)
	report, receivedAtMS, hasFull := s.fullReport(agent.AgentID)
	if !current.Partial {
		report, receivedAtMS, hasFull = current, now.UnixMilli(), true
	} else if hasFull && fullReportStale(receivedAtMS, now, fullFreshnessSeconds) {
		hasFull = false
	}
	allClients, err := s.clients.ListAll(ctx)
	if err != nil {
		return nodeprotocol.DirectivesBody{}, nodeprotocol.Envelope{}, fmt.Errorf("nodesync: list quota clients: %w", err)
	}
	agents, err := s.agents.List(ctx)
	if err != nil {
		return nodeprotocol.DirectivesBody{}, nodeprotocol.Envelope{}, fmt.Errorf("nodesync: list native agents: %w", err)
	}
	clientByID := make(map[int64]*domain.PSPClient, len(allClients))
	for _, client := range allClients {
		if client != nil {
			clientByID[client.ID] = client
		}
	}
	pendingByUser := s.pendingUsage(clientByID, agents)
	counters := make(map[int64]nodeprotocol.ClientCounters)
	if hasFull {
		for _, counter := range report.Clients {
			if id, parseErr := counter.Key.RowID(); parseErr == nil {
				counters[id] = counter
			}
		}
	}
	body := nodeprotocol.DirectivesBody{
		ForRosterVersion: rosterVersion,
		Quota:            make([]nodeprotocol.QuotaEntry, 0, len(snapshot.Clients)),
	}
	users := make(map[int64]*domain.User)
	for _, desired := range snapshot.Clients {
		client := desired.Client
		if client == nil {
			continue
		}
		user := users[client.UserID]
		if user == nil {
			user, err = s.users.GetByID(ctx, client.UserID)
			if err != nil {
				return nodeprotocol.DirectivesBody{}, nodeprotocol.Envelope{}, fmt.Errorf("nodesync: load user %d: %w", client.UserID, err)
			}
			users[client.UserID] = user
		}
		counter, counterKnown := counters[client.ID]
		baseline := int64(0)
		if counterKnown && counter.Present {
			baseline = nonNegativeSum(counter.UpBytes, counter.DownBytes)
		}
		entry := nodeprotocol.QuotaEntry{Client: nodeprotocol.NewClientKey(client.ID), BaselineBytes: baseline}
		if user.TrafficLimitBytes > 0 {
			// A limited client whose current cumulative counter is unknown is
			// closed until a full report proves the row exists. Treating unknown
			// as zero would hand out a fresh full-period grant after cache loss.
			remaining := int64(0)
			if hasFull && counterKnown && counter.Present {
				remaining = saturatingSub(user.TrafficLimitBytes, saturatingAdd(user.PeriodUsed(), pendingByUser[user.ID]))
			}
			entry.HeadroomBytes = int64Pointer(remaining)
			if periodEnd := nextPeriodEnd(periodNow, user.TrafficResetPeriod); !periodEnd.IsZero() {
				entry.PeriodEndsAtMS = periodEnd.UnixMilli()
				entry.NextPeriodHeadroomBytes = int64Pointer(user.TrafficLimitBytes)
			}
		}
		body.Quota = append(body.Quota, entry)
	}
	userIDs := make([]int64, 0, len(users))
	for id := range users {
		userIDs = append(userIDs, id)
	}
	sort.Slice(userIDs, func(i, j int) bool { return userIDs[i] < userIDs[j] })
	for _, id := range userIDs {
		body.IPShadow = append(body.IPShadow, nodeprotocol.IPShadowEntry{
			Subject: nodeprotocol.NewSubjectKey(id), IPLimit: users[id].IPLimit,
		})
	}
	coverage, oldestReportMS := s.aggregateCoverage(allClients, agents, fullFreshnessSeconds, now)
	body.Coverage = coverage
	envelope := nodeprotocol.Envelope{
		ComputedAtMS: now.UnixMilli(), NextPollSeconds: nextPollSeconds,
		FullReportSeconds: settings.FullReportSeconds, WantFullReport: !hasFull,
	}
	envelope.NumeratorAsOfMS, envelope.NumeratorOldestReportAgeMS,
		envelope.OverburnHeadroomBytes = s.recordGrantsAndFleetEnvelope(agent.AgentID, body.Quota, now, oldestReportMS)
	return body, envelope, nil
}

// aggregateCoverage derives the denominator from PSP's own native-panel client
// rows, never from however many counters happened to arrive. EntriesStale is a
// tag only: it must not reduce Entries or any quota/error bound.
func (s *Service) aggregateCoverage(clients []*domain.PSPClient, agents []*domain.NodeAgent, fullFreshnessSeconds int, now time.Time) (nodeprotocol.SegmentCounts, int64) {
	agentByPanel := make(map[int64]string, len(agents))
	for _, agent := range agents {
		if agent != nil {
			agentByPanel[agent.PanelID] = agent.AgentID
		}
	}
	s.mu.RLock()
	reports := make(map[string]receivedFullReport, len(s.reports))
	for agentID, received := range s.reports {
		received.report = cloneReport(received.report)
		reports[agentID] = received
	}
	s.mu.RUnlock()

	type reportIndex struct {
		stale    bool
		counters map[nodeprotocol.ClientKey]bool
		atMS     int64
	}
	indexed := make(map[string]reportIndex, len(agentByPanel))
	for _, agentID := range agentByPanel {
		received, ok := reports[agentID]
		idx := reportIndex{stale: !ok || received.receivedAtMS <= 0, atMS: received.receivedAtMS}
		idx.stale = idx.stale || fullReportStale(received.receivedAtMS, now, fullFreshnessSeconds)
		if received.receivedAtMS > now.UnixMilli() {
			// A PSP wall-clock rollback makes the age unknowable. Keep the rows
			// stale and publish an unknown aggregate timestamp instead of a
			// future numerator time with a deceptively zero age.
			idx.atMS = 0
		}
		idx.counters = make(map[nodeprotocol.ClientKey]bool, len(received.report.Clients))
		for _, counter := range received.report.Clients {
			idx.counters[counter.Key] = counter.Present
		}
		indexed[agentID] = idx
	}

	coverage := nodeprotocol.SegmentCounts{}
	subjects := make(map[int64]struct{})
	usedAgents := make(map[string]struct{})
	for _, client := range clients {
		if client == nil {
			continue
		}
		agentID, native := agentByPanel[client.PanelID]
		if !native {
			continue
		}
		coverage.Entries++
		subjects[client.UserID] = struct{}{}
		usedAgents[agentID] = struct{}{}
		idx := indexed[agentID]
		if idx.stale || !idx.counters[nodeprotocol.NewClientKey(client.ID)] {
			coverage.EntriesStale++
		}
	}
	coverage.Subjects = len(subjects)

	var oldest int64
	for agentID := range usedAgents {
		idx := indexed[agentID]
		if idx.atMS <= 0 {
			return coverage, 0
		}
		if oldest == 0 || idx.atMS < oldest {
			oldest = idx.atMS
		}
	}
	return coverage, oldest
}

// recordGrantsAndFleetEnvelope records the actual residual grants PSP most
// recently sent each agent, then reports fleet-wide exposure and the oldest
// full-report timestamp in the same critical section. A per-agent sum would
// look precise while hiding the exact P× overburn risk this metric exists to
// expose.
func (s *Service) recordGrantsAndFleetEnvelope(agentID string, quotas []nodeprotocol.QuotaEntry, now time.Time, oldest int64) (int64, int64, int64) {
	current := make(map[nodeprotocol.ClientKey]int64)
	for _, quota := range quotas {
		if quota.HeadroomBytes != nil {
			current[quota.Client] = *quota.HeadroomBytes
		}
	}
	nowMS := now.UnixMilli()
	s.mu.Lock()
	s.grants[agentID] = current
	var overburn int64
	for _, byClient := range s.grants {
		for _, headroom := range byClient {
			overburn = saturatingAdd(overburn, headroom)
		}
	}
	s.mu.Unlock()
	var age int64
	if oldest > 0 && nowMS > oldest {
		age = nowMS - oldest
	}
	return oldest, age, overburn
}

func (s *Service) fullReport(agentID string) (nodeprotocol.NodeReport, int64, bool) {
	s.mu.RLock()
	received, ok := s.reports[agentID]
	s.mu.RUnlock()
	return cloneReport(received.report), received.receivedAtMS, ok
}

// effectiveFullReportPeriod mirrors the runner's discrete polling schedule.
// A requested 45-second full interval on a 30-second poll actually produces a
// full report every 60 seconds; zero means every poll, not "never stale".
func effectiveFullReportPeriod(fullReportSeconds, pollSeconds int) int {
	if pollSeconds <= 0 {
		pollSeconds = defaultNextPollSeconds
	}
	if fullReportSeconds <= 0 {
		return pollSeconds
	}
	steps := (fullReportSeconds + pollSeconds - 1) / pollSeconds
	return steps * pollSeconds
}

func fullReportStale(receivedAtMS int64, now time.Time, freshnessSeconds int) bool {
	if receivedAtMS <= 0 || freshnessSeconds <= 0 {
		return true
	}
	nowMS := now.UnixMilli()
	return nowMS < receivedAtMS || nowMS-receivedAtMS > int64(freshnessSeconds)*1000
}

func (s *Service) pendingUsage(clients map[int64]*domain.PSPClient, agents []*domain.NodeAgent) map[int64]int64 {
	panelByAgent := make(map[string]int64, len(agents))
	for _, agent := range agents {
		if agent != nil {
			panelByAgent[agent.AgentID] = agent.PanelID
		}
	}
	s.mu.RLock()
	reports := make(map[string]nodeprotocol.NodeReport, len(s.reports))
	for agentID, received := range s.reports {
		reports[agentID] = cloneReport(received.report)
	}
	s.mu.RUnlock()
	result := make(map[int64]int64)
	for agentID, report := range reports {
		panelID, active := panelByAgent[agentID]
		if !active {
			continue
		}
		for _, counter := range report.Clients {
			id, err := counter.Key.RowID()
			if err != nil || !counter.Present {
				continue
			}
			client := clients[id]
			// Authentication scopes the request to one agent, but client keys are
			// still untrusted wire data. Bind every pending delta back to PSP's
			// durable agent→panel and client→panel ownership before it can affect
			// another user's fleet-wide quota.
			if client == nil || client.PanelID != panelID {
				continue
			}
			result[client.UserID] = saturatingAdd(result[client.UserID], s.pendingCounterDelta(client, counter))
		}
	}
	return result
}

func (s *Service) pendingCounterDelta(client *domain.PSPClient, counter nodeprotocol.ClientCounters) int64 {
	if counter.CounterEpoch == 0 {
		return 0
	}
	baseUp, baseDown := client.LastRawUpBytes, client.LastRawDownBytes
	s.mu.Lock()
	if client.LastCounterEpoch == counter.CounterEpoch {
		delete(s.anchors, client.ID)
	} else {
		anchor, exists := s.anchors[client.ID]
		if !exists || anchor.CounterEpoch != counter.CounterEpoch {
			s.anchors[client.ID] = counter
			s.mu.Unlock()
			return 0
		}
		baseUp, baseDown = anchor.UpBytes, anchor.DownBytes
	}
	s.mu.Unlock()
	up := counter.UpBytes - baseUp
	down := counter.DownBytes - baseDown
	if up < 0 {
		up = 0
	}
	if down < 0 {
		down = 0
	}
	return saturatingAdd(up, down)
}

func nextPeriodEnd(now time.Time, period domain.ResetPeriod) time.Time {
	switch period {
	case domain.ResetMonthly:
		return time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, now.Location())
	case domain.ResetQuarterly:
		month := time.Month(((int(now.Month())-1)/3)*3 + 1)
		return time.Date(now.Year(), month+3, 1, 0, 0, 0, 0, now.Location())
	case domain.ResetYearly:
		return time.Date(now.Year()+1, time.January, 1, 0, 0, 0, 0, now.Location())
	default:
		return time.Time{}
	}
}

func int64Pointer(value int64) *int64 { return &value }

func nonNegativeSum(a, b int64) int64 {
	if a < 0 {
		a = 0
	}
	if b < 0 {
		b = 0
	}
	return saturatingAdd(a, b)
}

func saturatingAdd(a, b int64) int64 {
	if b > 0 && a > math.MaxInt64-b {
		return math.MaxInt64
	}
	if b < 0 && a < math.MinInt64-b {
		return math.MinInt64
	}
	return a + b
}

func saturatingSub(limit, used int64) int64 {
	if used >= limit {
		return 0
	}
	return limit - used
}

// NativePanelSnapshot projects only a recent cached full observation from a
// live agent. A partial report never turns absent enumerations into deletion
// or zero traffic, and stale bytes never remain readable indefinitely.
func (s *Service) NativePanelSnapshot(ctx context.Context, panelID int64) (*ports.NativePanelSnapshot, error) {
	agent, err := s.agents.GetByPanelID(ctx, panelID)
	if err != nil {
		return nil, err
	}
	report, receivedAtMS, ok := s.fullReport(agent.AgentID)
	if !ok {
		return nil, domain.ErrNotFound
	}
	settings, err := s.settings.Load(ctx, ports.UISettings{
		NodePollSeconds: defaultNextPollSeconds, FullReportSeconds: defaultFullReportSecs,
	})
	if err != nil {
		return nil, fmt.Errorf("native panel snapshot: load cadence: %w", err)
	}
	pollSeconds := settings.NodePollSeconds
	if pollSeconds <= 0 {
		pollSeconds = defaultNextPollSeconds
	}
	now := s.now().UTC()
	// Liveness is based only on the durable receipt time of any heartbeat.
	// Three missed polls absorb ordinary jitter without letting a dead agent's
	// last snapshot look live forever.
	if agent.OfflineAt(now, 3*time.Duration(pollSeconds)*time.Second) {
		return nil, domain.ErrNotFound
	}
	// Full enumerations are independently paced. Allow one additional poll of
	// scheduling/network jitter beyond the effective discrete full cadence;
	// after that, absent objects and counters are unknowable, not healthy zeroes.
	fullFreshnessSeconds := effectiveFullReportPeriod(settings.FullReportSeconds, pollSeconds) + pollSeconds
	if fullReportStale(receivedAtMS, now, fullFreshnessSeconds) {
		return nil, domain.ErrNotFound
	}
	desired, err := s.desired.Load(ctx, panelID)
	if err != nil {
		return nil, err
	}
	configStream, err := s.agents.GetStream(ctx, agent.AgentID, domain.NodeAgentStreamConfig)
	if err != nil {
		return nil, err
	}
	rosterStream, err := s.agents.GetStream(ctx, agent.AgentID, domain.NodeAgentStreamRoster)
	if err != nil {
		return nil, err
	}
	configHave := report.Have[nodeprotocol.StreamConfig]
	rosterHave := report.Have[nodeprotocol.StreamRoster]
	configCurrent := configHave.Applied.Epoch == agent.Epoch && string(configHave.ETag) == configStream.DesiredETag
	rosterCurrent := rosterHave.Applied.Epoch == agent.Epoch && string(rosterHave.ETag) == rosterStream.DesiredETag
	statusByStream := make(map[string]map[string]nodeprotocol.ObjectStatus)
	statusByStream[nodeprotocol.StreamConfig] = objectStatuses(report.Objects, nodeprotocol.StreamConfig)
	statusByStream[nodeprotocol.StreamRoster] = objectStatuses(report.Objects, nodeprotocol.StreamRoster)
	listenerCounters := make(map[int64]nodeprotocol.ListenerCounters)
	for _, counter := range report.ListenerCounters {
		if id, parseErr := counter.Key.RowID(); parseErr == nil {
			listenerCounters[id] = counter
		}
	}
	clientCounters := make(map[int64]nodeprotocol.ClientCounters)
	for _, counter := range report.Clients {
		if id, parseErr := counter.Key.RowID(); parseErr == nil {
			clientCounters[id] = counter
		}
	}
	nodesByID := make(map[int64]*domain.Node, len(desired.Nodes))
	result := &ports.NativePanelSnapshot{
		Clients: make(map[string]ports.ClientDetail), LiveClientIPs: make(map[string][]string),
		Status: ports.ServerStatus{PanelVersion: report.AgentVersion, XrayVersion: report.CoreVersion, XrayState: report.CoreState},
	}
	for _, node := range desired.Nodes {
		if node == nil {
			continue
		}
		nodesByID[node.ID] = node
		counter, present := listenerCounters[node.ID]
		object := statusByStream[nodeprotocol.StreamConfig][string(nodeprotocol.NewListenerKey(node.ID))]
		if !configCurrent || !present || !counter.Present || object.State != nodeprotocol.ObjectApplied {
			continue
		}
		result.Inbounds = append(result.Inbounds, ports.Inbound{
			ID: node.InboundID, Up: counter.UpBytes, Down: counter.DownBytes,
			Total: nonNegativeSum(counter.UpBytes, counter.DownBytes), CounterEpoch: counter.CounterEpoch,
			Remark: node.InboundRemark,
			Enable: node.Enabled, ExpiryTime: node.InboundExpiryTime,
			Listen: node.InboundListen, Port: node.DesiredPort, Protocol: node.DesiredProtocol,
			Settings: node.InboundSettings, StreamSettings: node.StreamSettings,
			Sniffing: node.Sniffing, Allocate: node.Allocate,
		})
	}
	for _, desiredClient := range desired.Clients {
		client := desiredClient.Client
		if client == nil {
			continue
		}
		counter, present := clientCounters[client.ID]
		object := statusByStream[nodeprotocol.StreamRoster][string(nodeprotocol.NewClientKey(client.ID))]
		if !rosterCurrent || !present || !counter.Present || object.State != nodeprotocol.ObjectApplied {
			continue
		}
		detail := ports.ClientDetail{
			ID: client.UUID, Email: client.Email, Enable: client.DesiredEnable,
			Password: client.Password, Auth: client.UUID, ExpiryTime: client.DesiredExpiryTime,
			LimitIP: client.PanelIPLimit, LimitHwid: client.PanelDeviceLimit,
		}
		for _, attachment := range desiredClient.Inbounds {
			if node := nodesByID[attachment.NodeID]; node != nil {
				detail.InboundIDs = append(detail.InboundIDs, node.InboundID)
				if attachment.FlowOverride != "" {
					detail.Flow = attachment.FlowOverride
				}
			}
		}
		sort.Ints(detail.InboundIDs)
		result.Clients[client.Email] = detail
		if len(counter.LiveIPs) > 0 {
			ips := append([]string(nil), counter.LiveIPs...)
			sort.Strings(ips)
			result.LiveClientIPs[client.Email] = compactStrings(ips)
		}
		for i := range result.Inbounds {
			if containsInt(detail.InboundIDs, result.Inbounds[i].ID) {
				result.Inbounds[i].ClientStats = append(result.Inbounds[i].ClientStats, ports.ClientTraffic{
					InboundID: result.Inbounds[i].ID, Email: client.Email,
					Up: counter.UpBytes, Down: counter.DownBytes,
					Total:  nonNegativeSum(counter.UpBytes, counter.DownBytes),
					Enable: client.DesiredEnable, ExpiryTime: client.DesiredExpiryTime,
					CounterEpoch: counter.CounterEpoch,
				})
			}
		}
	}
	return result, nil
}

func compactStrings(items []string) []string {
	out := items[:0]
	for _, item := range items {
		if item == "" || (len(out) > 0 && out[len(out)-1] == item) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func containsInt(items []int, want int) bool {
	i := sort.SearchInts(items, want)
	return i < len(items) && items[i] == want
}

var _ ports.NativePanelSnapshotReader = (*Service)(nil)
