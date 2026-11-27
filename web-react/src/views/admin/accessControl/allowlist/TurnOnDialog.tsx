import { useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { destErrorOf, type DestRef } from '@/api/accessControl'
import { AsyncButton } from '@/components/common/AsyncButton'
import { pushSnack } from '@/components/feedback/toasts'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { IconCancel, IconInfo, IconSuccess, IconWarning } from '@/lib/icons'
import { cn } from '@/lib/utils'
import { useDestGroup, useDestGroupPreview, useDestStatus } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { applyMinutes } from '@/utils/accessControl'
import { ImpactDialogFrame } from '../../nodes/delete/ImpactDialogFrame'
import { minutesWords } from '../confirmCopy'
import { destErrorText } from '../destErrors'
import { blockedByLists } from './allowlistState'

const K = 'admin:access_control.allowlist.turn_on.'

type Tone = 'ok' | 'failing' | 'attention' | 'quiet'

const CHECK_ICON = {
  ok: <IconSuccess aria-hidden className="size-5 text-success" />,
  failing: <IconCancel aria-hidden className="size-5 text-destructive" />,
  attention: <IconWarning aria-hidden className="size-5 text-warning-ink" />,
  quiet: <IconInfo aria-hidden className="size-5 text-muted-foreground" />,
}

/** One line of the checklist: an icon and its words, never colour alone; `data-tone` is what tests read. */
function Check({ id, tone, children, action }: { id: string; tone: Tone; children: ReactNode; action?: ReactNode }) {
  return (
    <li data-testid={`check-${id}`} data-tone={tone} className="flex flex-wrap items-start gap-2">
      <span className="mt-px">{CHECK_ICON[tone]}</span>
      <span className={cn('min-w-0 flex-[1_1_12rem] text-sm [overflow-wrap:anywhere]', tone === 'quiet' && 'text-muted-foreground')}>{children}</span>
      {action}
    </li>
  )
}

export interface TurnOnDialogProps {
  open: boolean
  group: { id: number; name: string; members: number }
  /** The lists the allowlist will be made of (the chosen ones, saved or not). */
  listIds: readonly number[]
  /** The lists as saved, when the lists being turned on differ from them (an edit): what is saved no longer decides. Default: the same. */
  savedListIds?: readonly number[]
  /** The base hosts about to be saved with a group's first allowlist; with none yet the saved row says. */
  baseCount?: number
  /** The group already has an allowlist (a report to look at); a first one has none. */
  hasRow?: boolean
  /** Which day of report-only the group is on; null when it never was (no data to look at). */
  reportDay: number | null
  /** Makes the one write. A rejection is explained here. */
  onConfirm: () => Promise<void>
  onClose: () => void
  onViewReport: () => void
  onViewNodes: () => void
}

/**
 * TURNING AN ALLOWLIST ON (S11). Turning on is the one change that can leave
 * members unable to reach anything, so it shows a checklist first. Only a list
 * that has not been downloaded stops it (the server refuses it too, 409
 * dest_list_not_ready); everything else is a note the admin may weigh (D3).
 * The write itself is the caller's: this dialog asks once and says why a
 * refusal came back.
 */
export default function TurnOnDialog({ open, group, listIds, savedListIds, baseCount, hasRow = true, reportDay, onConfirm, onClose, onViewReport, onViewNodes }: TurnOnDialogProps) {
  const { t, i18n } = useTranslation(['admin'])
  const scope = useQueryScope()
  const status = useDestStatus(scope, { enabled: open })
  const detail = useDestGroup(scope, group.id, { enabled: open })
  const preview = useDestGroupPreview(scope, group.id, 'on', listIds, { enabled: open })
  const [refusal, setRefusal] = useState<string | null>(null)
  const num = new Intl.NumberFormat(i18n.language)

  const r = detail.data?.readiness
  const reading = detail.isPending
  const failed = detail.isError
  // The saved readiness speaks of the saved lists; once the lists were edited only what is chosen counts.
  const sameLists = !savedListIds || (savedListIds.length === listIds.length && savedListIds.every(id => listIds.includes(id)))
  const notReady: DestRef[] = []
  for (const l of [...(sameLists ? r?.not_ready_lists ?? [] : []), ...(preview.data?.not_ready_lists ?? [])]) {
    if (!notReady.some(x => x.id === l.id)) notReady.push(l)
  }
  const blocked = blockedByLists(notReady)
  const names = notReady.map(l => l.name).join(t(`${K}list_separator`))
  const topSites = (r?.top_sites ?? []).filter(s => !s.added).slice(0, 3).map(s => s.site).join(t(`${K}list_separator`))

  const confirm = async () => {
    setRefusal(null)
    try {
      await onConfirm()
    } catch (err) {
      const e = destErrorOf(err)
      if (e?.code === 'dest_group_stale') {
        pushSnack(t(`${K}stale`), 'warning')
        onClose()
      } else if (e?.code === 'dest_list_not_ready') {
        const list = ((e.body.not_ready_lists as DestRef[] | undefined) ?? []).map(l => l.name).filter(Boolean).join(t(`${K}list_separator`)) || names
        setRefusal(t(`${K}not_ready`, { list }))
      } else {
        setRefusal(destErrorText(t, err))
      }
    }
  }

  // Turning on is the one change that can leave members with nothing to reach, so focus opens on Cancel
  // (ImpactDialogFrame) and the confirm button is the destructive one.
  const cancelRef = useRef<HTMLButtonElement>(null)
  if (!open) return null
  return (
    <ImpactDialogFrame
      title={t(`${K}title`, { group: group.name })}
      cancelRef={cancelRef}
      cancelLabel={t(`${K}cancel`)}
      onClose={onClose}
      actions={(
        <AsyncButton variant="destructive" disabled={reading || failed || blocked || (!sameLists && (preview.isPending || preview.isFetching || preview.isError))} onClick={confirm}>
          {t(`${K}confirm`)}
        </AsyncButton>
      )}
    >
      {reading && <Skeleton aria-label={t(`${K}reading`)} role="progressbar" className="h-16 w-full" />}
      {failed && (
        <Alert variant="destructive">
          <AlertDescription className="flex flex-wrap items-center gap-2">
            <span className="flex-1">{t(`${K}read_failed`)}</span>
            <Button size="sm" variant="outline" onClick={() => void detail.refetch()}>{t('admin:access_control.retry')}</Button>
          </AlertDescription>
        </Alert>
      )}
      {r && (
        <ul className="m-0 grid list-none gap-2 p-0">
          {blocked
            ? <Check id="lists" tone="failing">{t(`${K}not_ready`, { list: names })}</Check>
            : <Check id="lists" tone="ok">{t(`${K}lists_ready`)}</Check>}
          {r.unapplied_nodes > 0 && (
            <Check id="unapplied" tone="attention"
              action={<Button size="sm" variant="outline" onClick={onViewNodes}>{t(`${K}view`)}</Button>}>
              {t(`${K}unapplied`, { count: r.unapplied_nodes })}
            </Check>
          )}
          {(baseCount !== undefined ? baseCount > 0 : r.base_nonempty)
            ? <Check id="base" tone="ok">{t(`${K}base_ok`)}</Check>
            : <Check id="base" tone="attention">{t(`${K}base_empty`)}</Check>}
          {r.remaining_sites_24h > 0
            ? (
              <Check id="remaining" tone="attention" action={hasRow ? <Button size="sm" variant="outline" onClick={onViewReport}>{t(`${K}view_report`)}</Button> : undefined}>
                {t(`${K}remaining`, { count: r.remaining_sites_24h, top: topSites })}
              </Check>
            )
            : <Check id="remaining" tone="ok">{t(`${K}remaining_none`)}</Check>}
          {reportDay !== null
            ? <Check id="age" tone="quiet">{t(`${K}age`, { days: reportDay })}</Check>
            : <Check id="age" tone="quiet">{t(`${K}age_none`)}</Check>}
        </ul>
      )}
      <div className="rounded-lg bg-destructive/10 p-3 text-sm">
        <p>{t(`${K}consequence`, { members: num.format(group.members), minutes: minutesWords(t, applyMinutes(status.data)) })}</p>
        {(preview.data?.nodes_lost.length ?? 0) > 0 && (
          <p className="mt-1">{t(`${K}lost`, { count: preview.data?.nodes_lost.length })}</p>
        )}
      </div>
      {refusal && <Alert variant="destructive"><AlertDescription>{refusal}</AlertDescription></Alert>}
    </ImpactDialogFrame>
  )
}
