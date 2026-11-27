import { useTranslation } from 'react-i18next'

import { SegmentedControl } from '@/components/form/SegmentedControl'

export type UsageBasis = 'charged' | 'raw'

/** Raw / charged switch shared by the per-server and per-node usage tables. */
export function UsageBasisToggle({ value, onChange }: { value: UsageBasis; onChange: (v: UsageBasis) => void }) {
  const { t } = useTranslation('admin')
  return (
    <SegmentedControl<UsageBasis>
      size="sm" value={value} onChange={onChange}
      aria-label={t('users.nodeusage.mode_label')}
      options={[
        { value: 'charged', label: t('users.nodeusage.mode_charged') },
        { value: 'raw', label: t('users.nodeusage.mode_raw') },
      ]}
    />
  )
}
