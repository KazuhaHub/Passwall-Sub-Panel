package ports

import "github.com/KazuhaHub/passwall-sub-panel/internal/domain"

// NodeTaskLifecyclePolicy returns the exact configured values. Consumers must
// Validate before using them; invalid stored policy must not become a shorter
// evidence lifetime through defaulting or coercion at a task/cleanup boundary.
func (s UISettings) NodeTaskLifecyclePolicy() domain.NodeTaskLifecyclePolicy {
	return domain.NodeTaskLifecyclePolicy{
		OfflineReconcileDays: s.NodeTaskOfflineReconcileDays,
		BackupRestoreDays:    s.NodeTaskBackupRestoreDays,
		ResultRetentionDays:  s.NodeTaskResultRetentionDays,
	}
}
