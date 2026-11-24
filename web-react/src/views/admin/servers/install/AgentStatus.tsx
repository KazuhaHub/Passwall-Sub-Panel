import { useTranslation } from 'react-i18next'
import type { NativeAgentStatus } from '@/api/servers'
import { Notice } from '@/components/common/Notice'

/**
 * What the agent is doing now and what to do next, in words. Three kinds of "not reporting" are different causes with
 * different actions (never connected, the panel itself just restarted, gone silent), so each has its own sentence.
 */
export function AgentStatus({ status, error }: { status: NativeAgentStatus | null; error: string }) {
  const { t } = useTranslation('admin')
  const state = status?.state ?? 'checking'
  const tone = status?.state === 'running' ? 'success' : status?.state === 'error' ? 'danger' : status?.state === 'offline' ? 'warning' : 'info'
  return (
    <section className="flex flex-col gap-2" aria-label={t('admin:servers.native.status_title')}>
      <h3 className="text-sm font-medium">{t('admin:servers.native.status_title')}</h3>
      {error && <Notice tone="warning">{t('admin:servers.native.status_stale')} {error}</Notice>}
      <Notice tone={tone}>
        <span className="block font-medium">{t(`admin:servers.native.agent_status.${state}`)}</span>
        <span className="block text-sm">{t(`admin:servers.native.status_next.${state}`)}</span>
      </Notice>
    </section>
  )
}
