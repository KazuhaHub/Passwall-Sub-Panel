package domain

import "fmt"

// MaxNodeTaskLifecycleDays is an operational input bound, not a claim that a
// ten-year-old backup can be restored automatically. Keeping it bounded also
// leaves ample room for checked duration/deadline arithmetic at task creation.
const MaxNodeTaskLifecycleDays = 10 * 365

// NodeTaskLifecyclePolicy is the control plane's task-evidence policy, in
// elapsed 24-hour days. These are NOT execution TTLs, proxy leases or permission
// to replay an operation whose outcome is unknown. Each production task kind
// must separately define its latest-start and recovery contracts.
//
// Settings select the policy for NEW tasks. A future task producer must retain
// its policy snapshot and original authorization deadline; a cleaner must not
// derive an existing task's safety deadline from today's mutable settings.
// Identity tombstones have no automatic physical-deletion policy in v1.
type NodeTaskLifecyclePolicy struct {
	OfflineReconcileDays int
	BackupRestoreDays    int
	ResultRetentionDays  int
}

func DefaultNodeTaskLifecyclePolicy() NodeTaskLifecyclePolicy {
	return NodeTaskLifecyclePolicy{
		OfflineReconcileDays: 30,
		BackupRestoreDays:    30,
		ResultRetentionDays:  90,
	}
}

// Validate rejects inconsistent support promises rather than silently reducing
// a restore/offline window or increasing an administrator's retention choice.
// Zero has no "unlimited execution" or "delete immediately" interpretation.
func (p NodeTaskLifecyclePolicy) Validate() error {
	for _, field := range []struct {
		name string
		days int
	}{
		{"node_task_offline_reconcile_days", p.OfflineReconcileDays},
		{"node_task_backup_restore_days", p.BackupRestoreDays},
		{"node_task_result_retention_days", p.ResultRetentionDays},
	} {
		if field.days < 1 || field.days > MaxNodeTaskLifecycleDays {
			return fmt.Errorf("%w: %s must be between 1 and %d days", ErrValidation, field.name, MaxNodeTaskLifecycleDays)
		}
	}
	if p.ResultRetentionDays < p.OfflineReconcileDays || p.ResultRetentionDays < p.BackupRestoreDays {
		return fmt.Errorf("%w: node_task_result_retention_days must be at least both the offline reconciliation and backup restore windows", ErrValidation)
	}
	return nil
}
