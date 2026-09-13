package sqlstore

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/KazuhaHub/passwall-node/corecatalog"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// This adapter is not a pool mutation. Its caller must stop all PSP instances
// OR drain the single-instance live operation gate before Apply, and replace
// the pool before releasing admission. Old responses and detached writers must
// not commit after conversion or create new old-backend tasks after retirement.
type serverMigrationRepo struct{ db *gorm.DB }

func (r *serverMigrationRepo) Load(ctx context.Context, panelID int64) (*domain.ServerMigrationSnapshot, error) {
	if panelID <= 0 {
		return nil, fmt.Errorf("%w: server ID must be positive", domain.ErrValidation)
	}
	var snapshot *domain.ServerMigrationSnapshot
	err := r.db.WithContext(ctx).Session(&gorm.Session{Logger: logger.Discard}).Transaction(func(tx *gorm.DB) error {
		var err error
		snapshot, err = loadServerMigrationSnapshot(tx, panelID, false)
		return err
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, safeNativeCredentialStorageError(err)
	}
	return snapshot, nil
}

func (r *serverMigrationRepo) Apply(ctx context.Context, panelID int64, expectedFingerprint string, agent *domain.NodeAgent, credential string) error {
	if panelID <= 0 || expectedFingerprint == "" || agent == nil || agent.ID != 0 || agent.PanelID != panelID ||
		(agent.Epoch != 0 && agent.Epoch != 1) || agent.LastSeen != nil || agent.ObservedCoreEngine != "" {
		return fmt.Errorf("%w: offline conversion requires a new agent for the existing server", domain.ErrValidation)
	}
	copyAgent := *agent
	if err := validateNewNodeAgent(&copyAgent); err != nil {
		return fmt.Errorf("%w: invalid new node agent", domain.ErrValidation)
	}
	if copyAgent.DesiredCoreEngine != domain.NodeCoreXray {
		return fmt.Errorf("%w: conversion currently requires the Xray core", domain.ErrValidation)
	}
	release, err := corecatalog.Resolve(string(domain.NodeCoreXray), copyAgent.DesiredCoreVersion)
	if err != nil || release.Version != copyAgent.DesiredCoreVersion || !release.Selectable ||
		!release.Evidence.SourceAudited || !release.Evidence.ConfigTested || !release.Evidence.HandshakeTested ||
		(release.Tier != corecatalog.TierRecommended && release.Tier != corecatalog.TierVerified && release.Tier != corecatalog.TierRestricted) ||
		release.RequiresConfirmation != copyAgent.AllowRestrictedReality {
		// The preview canonicalizes an unnecessary acknowledgement to false.
		// Persist the same exact shape nodesync accepts, not just a checkbox.
		return fmt.Errorf("%w: exact verified core version and matching restriction acknowledgement required", domain.ErrValidation)
	}
	digest, err := nativeCredentialDigest(credential)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(digest), []byte(copyAgent.CredentialSHA256)) != 1 {
		return fmt.Errorf("%w: node credential does not match its verifier", domain.ErrConflict)
	}
	ciphertext, err := encryptNativeCredential(credential)
	if err != nil {
		return safeNativeCredentialStorageError(err)
	}
	var created *nodeAgentRow
	err = r.db.WithContext(ctx).Session(&gorm.Session{Logger: logger.Discard}).Transaction(func(tx *gorm.DB) error {
		snapshot, err := loadServerMigrationSnapshot(tx, panelID, true)
		if err != nil {
			return err
		}
		if snapshot.Panel.Kind != domain.PanelKind3XUI {
			return fmt.Errorf("%w: server is no longer a 3X-UI backend", domain.ErrConflict)
		}
		if len(snapshot.Blockers()) != 0 {
			return fmt.Errorf("%w: saved server configuration is not eligible for conversion", domain.ErrValidation)
		}
		fingerprint := snapshot.Fingerprint(copyAgent.DesiredCoreVersion, copyAgent.AllowRestrictedReality)
		if subtle.ConstantTimeCompare([]byte(expectedFingerprint), []byte(fingerprint)) != 1 {
			return fmt.Errorf("%w: saved server configuration changed since preview", domain.ErrConflict)
		}
		var identities int64
		if err := tx.Model(&nodeAgentRow{}).Where("panel_id = ? OR agent_id = ? OR credential_sha256 = ?",
			panelID, copyAgent.AgentID, digest).Count(&identities).Error; err != nil {
			return err
		}
		if identities != 0 {
			return fmt.Errorf("%w: server or agent already has a native identity", domain.ErrConflict)
		}
		nodeIDs := make([]int64, len(snapshot.Nodes))
		for i, node := range snapshot.Nodes {
			nodeIDs[i] = node.ID
		}
		// Resolve all creation payloads before any write. Their TargetID is zero,
		// so filtering by existing NodeIDs would silently miss stale creates.
		taskIDs, err := serverMigrationTaskIDs(tx, panelID, nodeIDs)
		if err != nil {
			return err
		}
		result := tx.Model(&xuiPanelRow{}).Where("id = ? AND kind IN ?", panelID,
			[]string{"", string(domain.PanelKind3XUI)}).Updates(map[string]any{
			"kind": string(domain.PanelKindPSP), "url": "psp://" + copyAgent.AgentID,
			"api_token": "", "username": "", "password": "", "auth_method": "",
			"insecure_skip_verify": false, "panel_version": "", "xray_version": "",
			"version_checked_at": nil, "ip_limit_enforcement": "", "ip_limit_probed_at": nil,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("%w: server backend changed concurrently", domain.ErrConflict)
		}
		created, err = createNodeAgentRows(tx, &copyAgent)
		if err != nil {
			return err
		}
		if err := tx.Model(&nodeAgentRow{}).Where("id = ?", created.ID).
			Update("credential_ciphertext", ciphertext).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		if len(nodeIDs) != 0 {
			// Only convergence evidence changes. Connection configuration, node
			// identity, tags, layout and all traffic/history columns stay intact.
			if err := tx.Model(&nodeRow{}).Where("id IN ? AND panel_id = ?", nodeIDs, panelID).
				Updates(map[string]any{"config_sync_state": domain.ConfigSyncPending,
					"config_pending_since": gorm.Expr("COALESCE(config_pending_since, ?)", now)}).Error; err != nil {
				return err
			}
			// Keep AppliedEmail/UUID/Password: subscriptions must not publish a
			// replacement credential merely because the new backend is pending.
			if err := tx.Model(&pspClientInboundRow{}).Where("node_id IN ?", nodeIDs).
				Updates(map[string]any{"state": string(domain.ClientApplyPending), "applied_version": 0,
					"first_failed_at": nil}).Error; err != nil {
				return err
			}
		}
		if len(taskIDs) != 0 {
			if err := tx.Model(&syncTaskRow{}).Where("id IN ? AND status <> ?", taskIDs, string(domain.SyncTaskRetired)).
				Updates(map[string]any{"status": string(domain.SyncTaskRetired),
					"finished_at": gorm.Expr("COALESCE(finished_at, ?)", now)}).Error; err != nil {
				return err
			}
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return safeNativeCredentialStorageError(err)
	}
	applyCreatedNodeAgent(agent, created)
	return nil
}

func migrationRead(tx *gorm.DB, lock bool) *gorm.DB {
	if lock {
		return tx.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	return tx
}

func loadServerMigrationSnapshot(tx *gorm.DB, panelID int64, lock bool) (*domain.ServerMigrationSnapshot, error) {
	var panelRow xuiPanelRow
	if err := migrationRead(tx, lock).First(&panelRow, panelID).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	panel, err := panelRow.toDomain()
	if err != nil {
		return nil, err
	}
	snapshot := &domain.ServerMigrationSnapshot{Panel: panel, Nodes: []*domain.Node{}, Clients: []domain.ServerMigrationClient{}}
	var nodes []nodeRow
	if err := migrationRead(tx, lock).Where("panel_id = ?", panelID).Order("id").Find(&nodes).Error; err != nil {
		return nil, err
	}
	nodeIDs := make([]int64, len(nodes))
	for i := range nodes {
		node, err := nodes[i].toDomain()
		if err != nil {
			return nil, err
		}
		snapshot.Nodes = append(snapshot.Nodes, node)
		nodeIDs[i] = node.ID
	}
	q := migrationRead(tx, lock).Where("panel_id = ?", panelID)
	if len(nodeIDs) != 0 {
		q = q.Or("id IN (?)", tx.Model(&pspClientInboundRow{}).Select("client_id").Where("node_id IN ?", nodeIDs))
	}
	var clients []pspClientRow
	if err := q.Order("id").Find(&clients).Error; err != nil {
		return nil, err
	}
	clientIDs := make([]int64, len(clients))
	for i := range clients {
		clientIDs[i] = clients[i].ID
	}
	var attachments []pspClientInboundRow
	if len(clientIDs) != 0 || len(nodeIDs) != 0 {
		q = migrationRead(tx, lock).Where("client_id IN ?", clientIDs)
		if len(nodeIDs) != 0 {
			q = q.Or("node_id IN ?", nodeIDs)
		}
		if err := q.Order("client_id, node_id").Find(&attachments).Error; err != nil {
			return nil, err
		}
	}
	byClient := make(map[int64][]domain.PSPClientInbound, len(clients))
	for _, row := range attachments {
		byClient[row.ClientID] = append(byClient[row.ClientID], domain.PSPClientInbound{
			ClientID: row.ClientID, NodeID: row.NodeID, FlowOverride: row.FlowOverride,
			State: domain.ClientApplyState(row.State), AppliedVersion: row.AppliedVersion,
			AppliedEmail: row.AppliedEmail, AppliedUUID: row.AppliedUUID, AppliedPassword: row.AppliedPassword,
			FirstFailedAt: row.FirstFailedAt,
		})
	}
	for i := range clients {
		snapshot.Clients = append(snapshot.Clients, domain.ServerMigrationClient{
			Client: rowToPSPClient(&clients[i]), Inbounds: byClient[clients[i].ID],
		})
		delete(byClient, clients[i].ID)
	}
	// A dangling client reference must remain visible to policy rather than
	// disappear through an INNER JOIN and masquerade as a complete closure.
	for _, row := range attachments {
		if orphan, exists := byClient[row.ClientID]; exists {
			snapshot.Clients = append(snapshot.Clients, domain.ServerMigrationClient{Inbounds: orphan})
			delete(byClient, row.ClientID)
		}
	}
	// Do not probe a missing legacy table inside PostgreSQL's transaction:
	// even a swallowed missing-table error leaves that transaction aborted.
	tables, err := tx.Migrator().GetTables()
	if err != nil {
		return nil, err
	}
	for _, table := range tables {
		if table == (ownershipRow{}).TableName() {
			if err := tx.Model(&ownershipRow{}).Where("panel_id = ?", panelID).
				Count(&snapshot.LegacyOwnershipCount).Error; err != nil {
				return nil, err
			}
			break
		}
	}
	return snapshot, nil
}

func serverMigrationTaskIDs(tx *gorm.DB, panelID int64, nodeIDs []int64) ([]int64, error) {
	match := tx.Where("type = ?", string(domain.SyncTaskNodeCreate))
	if len(nodeIDs) != 0 {
		match = match.Or("type IN ? AND target_id IN ?", []string{string(domain.SyncTaskNodeDelete),
			string(domain.SyncTaskNodeSetEnabled), string(domain.SyncTaskNodeUpdate)}, nodeIDs)
	}
	var tasks []syncTaskRow
	if err := migrationRead(tx, true).Where(match).Where("status <> ?", string(domain.SyncTaskRetired)).Order("id").Find(&tasks).Error; err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(tasks))
	for _, task := range tasks {
		if task.Type == string(domain.SyncTaskNodeCreate) {
			target, err := migrationCreatePanelID(task.Payload)
			if err != nil {
				return nil, fmt.Errorf("%w: node creation task %d has unresolved server identity", domain.ErrConflict, task.ID)
			}
			if target != panelID {
				continue
			}
		}
		ids = append(ids, task.ID)
	}
	return ids, nil
}

func migrationCreatePanelID(payload string) (int64, error) {
	var node json.RawMessage
	err := migrationJSONObject([]byte(payload), func(name string, value json.RawMessage) error {
		if strings.EqualFold(name, "node") {
			node = value
		}
		return nil
	})
	if err != nil || len(node) == 0 {
		return 0, errors.New("unresolved node creation identity")
	}
	var panelID int64
	seen := false
	err = migrationJSONObject(node, func(name string, value json.RawMessage) error {
		if strings.EqualFold(strings.ReplaceAll(name, "_", ""), "PanelID") {
			if seen {
				return errors.New("ambiguous server identity")
			}
			seen = true
			return json.Unmarshal(value, &panelID)
		}
		return nil
	})
	if err != nil || !seen || panelID <= 0 {
		return 0, errors.New("unresolved node creation identity")
	}
	return panelID, nil
}

func migrationJSONObject(raw []byte, field func(string, json.RawMessage) error) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return errors.New("object required")
	}
	seen := make(map[string]bool)
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return err
		}
		name, ok := key.(string)
		if !ok || seen[strings.ToLower(name)] {
			return errors.New("ambiguous object field")
		}
		seen[strings.ToLower(name)] = true
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return err
		}
		if err := field(name, value); err != nil {
			return err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("trailing object data")
	}
	return nil
}

var _ ports.ServerMigrationRepo = (*serverMigrationRepo)(nil)
