import { useTranslation } from 'react-i18next'
import type { ProbeState } from '../useServerProbes'

/** The last connection test, in words: reached (with how many inbounds), not reached (with the reason), or not tested yet. */
export function TestResult({ probe }: { probe?: ProbeState }) {
  const { t } = useTranslation('admin')
  if (!probe || probe.status === 'unknown') return <span className="text-sm text-muted-foreground">{t('admin:servers.status.unknown')}</span>
  if (probe.status === 'checking') return <span role="status" className="text-sm text-muted-foreground">{t('admin:servers.status.checking')}</span>
  if (probe.status === 'ok') return <span role="status" className="text-sm">{typeof probe.inbound_count === 'number' ? t('admin:servers.status.ok_count', { count: probe.inbound_count }) : t('admin:servers.status.ok')}</span>
  if (probe.status === 'unconfigured') return <span className="text-sm text-muted-foreground">{t('admin:servers.status.unconfigured')}</span>
  return <span role="alert" className="text-sm text-destructive">{t('admin:servers.status.fail')}{probe.error ? `: ${probe.error}` : ''}</span>
}
