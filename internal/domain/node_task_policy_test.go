package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestNodeTaskLifecyclePolicyDefaultsAndBounds(t *testing.T) {
	want := NodeTaskLifecyclePolicy{OfflineReconcileDays: 30, BackupRestoreDays: 30, ResultRetentionDays: 90}
	if got := DefaultNodeTaskLifecyclePolicy(); got != want {
		t.Fatalf("default = %+v, want %+v", got, want)
	}
	for _, policy := range []NodeTaskLifecyclePolicy{
		want,
		{OfflineReconcileDays: 1, BackupRestoreDays: 1, ResultRetentionDays: 1},
		{OfflineReconcileDays: 7, BackupRestoreDays: 45, ResultRetentionDays: 45},
		{OfflineReconcileDays: 60, BackupRestoreDays: 14, ResultRetentionDays: 120},
		{OfflineReconcileDays: 3650, BackupRestoreDays: 3650, ResultRetentionDays: 3650},
	} {
		if err := policy.Validate(); err != nil {
			t.Errorf("valid policy %+v: %v", policy, err)
		}
	}
	copy := DefaultNodeTaskLifecyclePolicy()
	copy.ResultRetentionDays = 1
	if got := DefaultNodeTaskLifecyclePolicy(); got != want {
		t.Fatalf("mutating returned default changed later defaults: %+v", got)
	}
}

func TestNodeTaskLifecyclePolicyRejectsInvalidPromises(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*NodeTaskLifecyclePolicy)
		field  string
	}{
		{"zero_offline", func(p *NodeTaskLifecyclePolicy) { p.OfflineReconcileDays = 0 }, "node_task_offline_reconcile_days"},
		{"zero_backup", func(p *NodeTaskLifecyclePolicy) { p.BackupRestoreDays = 0 }, "node_task_backup_restore_days"},
		{"zero_retention", func(p *NodeTaskLifecyclePolicy) { p.ResultRetentionDays = 0 }, "node_task_result_retention_days"},
		{"negative_offline", func(p *NodeTaskLifecyclePolicy) { p.OfflineReconcileDays = -1 }, "node_task_offline_reconcile_days"},
		{"negative_backup", func(p *NodeTaskLifecyclePolicy) { p.BackupRestoreDays = -1 }, "node_task_backup_restore_days"},
		{"negative_retention", func(p *NodeTaskLifecyclePolicy) { p.ResultRetentionDays = -1 }, "node_task_result_retention_days"},
		{"large_offline", func(p *NodeTaskLifecyclePolicy) { p.OfflineReconcileDays = MaxNodeTaskLifecycleDays + 1 }, "node_task_offline_reconcile_days"},
		{"large_backup", func(p *NodeTaskLifecyclePolicy) { p.BackupRestoreDays = MaxNodeTaskLifecycleDays + 1 }, "node_task_backup_restore_days"},
		{"large_retention", func(p *NodeTaskLifecyclePolicy) { p.ResultRetentionDays = MaxNodeTaskLifecycleDays + 1 }, "node_task_result_retention_days"},
		{"overflow_input", func(p *NodeTaskLifecyclePolicy) { p.ResultRetentionDays = int(^uint(0) >> 1) }, "node_task_result_retention_days"},
		{"short_for_offline", func(p *NodeTaskLifecyclePolicy) { p.OfflineReconcileDays = 91 }, "node_task_result_retention_days"},
		{"short_for_backup", func(p *NodeTaskLifecyclePolicy) { p.BackupRestoreDays = 91 }, "node_task_result_retention_days"},
	} {
		t.Run(test.name, func(t *testing.T) {
			policy := DefaultNodeTaskLifecyclePolicy()
			test.change(&policy)
			before := policy
			err := policy.Validate()
			if !errors.Is(err, ErrValidation) || !strings.Contains(err.Error(), test.field) {
				t.Fatalf("validate %+v: %v, want ErrValidation naming %s", policy, err, test.field)
			}
			if policy != before {
				t.Fatal("validation silently changed policy")
			}
		})
	}
}
