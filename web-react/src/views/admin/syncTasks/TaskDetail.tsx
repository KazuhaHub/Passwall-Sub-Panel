import type { SyncTask } from '@/api/types'
import { CodeSection, DetailList, DetailRow, TimeCell, useAdminT } from '../logs/shared'
import { attemptsOf, createdOf, finishedOf, lastErrorOf, nextRunOf, summaryOf, targetOf, typeOf, updatedOf } from './model'

/**
 * One task in full: what it is, when it runs next, the last error unabridged, and the stored record
 * (payload included) to copy into a bug report.
 */
export function TaskDetail({ task }: { task: SyncTask }) {
  const t = useAdminT()
  const type = typeOf(task)
  const error = lastErrorOf(task)
  return (
    <>
      <DetailList>
        <DetailRow label={t('sync_tasks.table.type')}>{type ? t(`sync_tasks.type.${type}`) : '—'}</DetailRow>
        <DetailRow label={t('sync_tasks.table.target')} mono>{targetOf(task)}</DetailRow>
        <DetailRow label={t('sync_tasks.table.summary')}>{summaryOf(task) || '—'}</DetailRow>
        <DetailRow label={t('sync_tasks.table.attempts')}>{attemptsOf(task)}</DetailRow>
        <DetailRow label={t('sync_tasks.table.next_run')}><TimeCell value={nextRunOf(task)} /></DetailRow>
        <DetailRow label={t('sync_tasks.detail.created')}><TimeCell value={createdOf(task)} /></DetailRow>
        <DetailRow label={t('sync_tasks.detail.updated')}><TimeCell value={updatedOf(task)} /></DetailRow>
        <DetailRow label={t('sync_tasks.detail.finished')} hidden={!finishedOf(task)}><TimeCell value={finishedOf(task)} /></DetailRow>
      </DetailList>
      {error && <CodeSection title={t('sync_tasks.table.error')} text={error} name={t('sync_tasks.table.error')} />}
      <CodeSection title={t('sync_tasks.detail.record')} text={JSON.stringify(task, null, 2)} name={t('sync_tasks.detail.record')} />
    </>
  )
}
