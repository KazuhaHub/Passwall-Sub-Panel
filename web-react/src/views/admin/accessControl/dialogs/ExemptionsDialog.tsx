import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { type DestExemption } from '@/api/accessControl'
import { AccessBadge } from '@/components/access/AccessBadge'
import { MoreMenu } from '@/components/common/MoreMenu'
import { confirm } from '@/components/ConfirmHost'
import { DetailDialog } from '@/components/data/DetailDialog'
import { EmptyState } from '@/components/feedback/EmptyState'
import { ErrorState } from '@/components/feedback/ErrorState'
import { pushSnack } from '@/components/feedback/toasts'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { IconDelete, IconEdit, IconUser } from '@/lib/icons'
import { useDeleteDestExemption, useDestExemptions, useDestStatus } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { applyMinutes, type AccessToneKind } from '@/utils/accessControl'
import { formatRelativeTimeShort } from '@/utils/relativeTime'
import { timeLeft } from '../timeLeft'
import { removeExemptionConfirm } from '../confirmCopy'
import { reportDestError } from '../destErrors'
import AddExemptionDialog from './AddExemptionDialog'

export interface ExemptionsDialogProps {
  open: boolean
  onClose: () => void
  /** Hands over to the account drawer: replaces `sheet=exemptions` with `user=<id>`. */
  onOpenUser: (userId: number) => void
}

const X = 'admin:access_control.exemptions.'
const HOUR = 3_600_000

/** S5: every exemption in one place, each with who added it, why and when it ends. */
export default function ExemptionsDialog({ open, onClose, onOpenUser }: ExemptionsDialogProps) {
  const { t } = useTranslation(['admin'])
  const scope = useQueryScope()
  const q = useDestExemptions(scope, { enabled: open })
  const status = useDestStatus(scope, { enabled: open })
  const remove = useDeleteDestExemption(scope)
  const [dialog, setDialog] = useState<{ open: boolean; existing?: DestExemption }>({ open: false })
  const now = Date.now()

  // Expired rows wait for the hourly cleanup: last, in the measuring tone.
  const rows = useMemo(() => [...(q.data ?? [])].sort((a, b) => Number(a.expired) - Number(b.expired)), [q.data])

  const tone = (r: DestExemption): { kind: AccessToneKind; label: string } => {
    if (r.expired) return { kind: 'measuring', label: t(`${X}expired`) }
    if (r.expires_at === null) return { kind: 'permanent', label: t(`${X}permanent`) }
    const ms = r.expires_at - now
    return { kind: ms < 24 * HOUR ? 'attention' : 'none', label: t(`${X}expires_in`, { left: timeLeft(t, ms) }) }
  }

  const doRemove = async (row: DestExemption) => {
    // Taking an exemption away puts that account back under every rule: focus starts on Cancel.
    const ok = await confirm({ ...removeExemptionConfirm({ t, minutes: applyMinutes(status.data) }, row.upn), destructive: true, initialFocus: 'cancel' })
    if (!ok) return
    try {
      await remove.mutateAsync(row.user_id)
      pushSnack(t(`${X}removed_snack`, { upn: row.upn }), 'success')
    } catch (err) {
      reportDestError(t, err)
    }
  }

  return (
    <>
      <DetailDialog open={open} onClose={onClose} size="md" title={t(`${X}title`)} description={t(`${X}lead`)}
        actions={<Button size="sm" onClick={() => setDialog({ open: true })}>{t(`${X}add`)}</Button>}>
        {q.isPending && <div className="grid gap-2">{[0, 1, 2].map(i => <Skeleton key={i} className="h-16 w-full" />)}</div>}
        {q.isError && <ErrorState compact title={t(`${X}load_failed`)} reason={q.error} retry={() => q.refetch()} />}
        {q.isSuccess && rows.length === 0 && <EmptyState compact title={t(`${X}empty`)} />}
        {rows.length > 0 && (
          <ul className="divide-y rounded-lg border">
            {rows.map(r => {
              const tn = tone(r)
              return (
                <li key={r.user_id} data-testid="exemption-row" className="flex items-start gap-2 p-3">
                  <div className="min-w-0 flex-1">
                    <p className="font-semibold [overflow-wrap:anywhere]" title={r.upn}>{r.upn || `#${r.user_id}`}</p>
                    <p className="text-sm text-muted-foreground [overflow-wrap:anywhere]">
                      {t(`${X}reason_line`, { reason: r.reason, by: r.created_by_upn || '—', ago: formatRelativeTimeShort(now - r.created_at, t) })}
                    </p>
                  </div>
                  <span title={t(`${X}expiry_tip`)} className="shrink-0"><AccessBadge kind={tn.kind} label={tn.label} /></span>
                  <MoreMenu label={t(`${X}row_menu`)} items={[
                    { id: 'open', label: t(`${X}open_user`), icon: IconUser, onSelect: () => onOpenUser(r.user_id) },
                    { id: 'edit', label: t(`${X}edit`), icon: IconEdit, onSelect: () => setDialog({ open: true, existing: r }) },
                    { id: 'cancel', label: t(`${X}cancel`), icon: IconDelete, destructive: true, onSelect: () => void doRemove(r) },
                  ]} />
                </li>
              )
            })}
          </ul>
        )}
        {/* Inside the dialog, so the two nest: the upper one is centred, traps focus and closes alone on Esc. */}
        <AddExemptionDialog open={dialog.open} existing={dialog.existing} onClose={() => setDialog({ open: false })} />
      </DetailDialog>
    </>
  )
}
