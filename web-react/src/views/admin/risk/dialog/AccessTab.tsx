import { useState, type ReactNode } from 'react'
import { useNavigate } from 'react-router'

import type { DestExemption, DestState, DestUserHit } from '@/api/accessControl'
import { AsyncButton } from '@/components/common/AsyncButton'
import { StatusBadge } from '@/components/common/StatusBadge'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/feedback/toasts'
import { ErrorState } from '@/components/feedback/ErrorState'
import { CardSkeleton } from '@/components/feedback/LoadingState'
import { Button } from '@/components/ui/button'
import { IconChevronRight, IconInfo } from '@/lib/icons'
import { useDeleteDestExemption, useDestStatus, useDestUser } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { useSiteStore } from '@/stores/site'
import { applyMinutes } from '@/utils/accessControl'
import { formatMsDualTz } from '@/utils/datetime'
import { formatRelativeTimeShort } from '@/utils/relativeTime'
import { stateDay } from '@/views/admin/accessControl/allowlist/allowlistState'
import { reportDestError } from '../../accessControl/destErrors'
import { removeExemptionConfirm } from '../../accessControl/confirmCopy'
import { recordsHref } from '../../accessControl/records/recordsParams'
import AddExemptionDialog from '../../accessControl/dialogs/AddExemptionDialog'
import { timeLeft } from '../../accessControl/timeLeft'
import { useRiskT } from '../useRiskT'
import UsageSection from './UsageSection'

const D = 'admin:access_control.drawer.'

function Row({ label, children, action, tinted }: { label: string; children: ReactNode; action?: ReactNode; tinted?: boolean }) {
  return (
    <div className={`flex flex-wrap items-center gap-3 rounded-lg border p-3 ${tinted ? 'bg-muted/50' : ''}`}>
      <span className="min-w-12 text-[13px] text-muted-foreground">{label}</span>
      <div className="min-w-0 flex-1 text-sm [overflow-wrap:anywhere]">{children}</div>
      {action}
    </div>
  )
}

/** A group's allowlist state as a badge, in the groups page's words: report-only counts its days, on says
 *  so. Drawn here with the new badge; the access-control page's own badge is still the old component set. */
function GroupStateBadge({ state, day }: { state: DestState; day: number | null }) {
  const { t } = useRiskT()
  const label = state === 'report' && day !== null
    ? t('admin:groups.destination.report', { days: day })
    : t(`admin:groups.destination.${state === 'report' ? 'report_nodays' : 'on'}`)
  return <span data-testid="allowlist-state" data-state={state}><StatusBadge tone={state === 'report' ? 'info' : 'neutral'} label={label} quiet={false} /></span>
}

/**
 * The drawer's access tab: the account's group, its exemption and the hits it
 * had in the window and, on request, its per-site usage. Read only while the
 * tab is open — the parent mounts it on selection — and the exemption can be
 * added or lifted in place, through the same mutations the exemptions sheet uses.
 */
export default function AccessTab({ userId }: { userId: number }) {
  const { t } = useRiskT()
  const scope = useQueryScope()
  const panelTz = useSiteStore(s => s.timezone)
  const q = useDestUser(scope, userId)
  const status = useDestStatus(scope)
  const remove = useDeleteDestExemption(scope)
  const navigate = useNavigate()
  const [adding, setAdding] = useState(false)

  if (q.isPending) return <div data-testid="access-tab-loading"><CardSkeleton lines={3} /></div>
  if (q.isError || !q.data) {
    return <ErrorState compact title={t(`${D}load_failed`)} retry={() => q.refetch()} />
  }

  const { group, exemption } = q.data
  const now = Date.now()
  const expiry = (e: DestExemption) => e.expires_at === null
    ? t(`${D}permanent`)
    : t(`${D}expires_in`, { left: timeLeft(t, Math.max(0, e.expires_at - now)) })

  const lift = async (e: DestExemption) => {
    const ok = await confirm({ ...removeExemptionConfirm({ t, minutes: applyMinutes(status.data) }, e.upn), initialFocus: 'cancel' })
    if (!ok) return
    try {
      await remove.mutateAsync(e.user_id)
      pushSnack(t(`${D}removed_snack`, { upn: e.upn }), 'success')
    } catch (err) {
      reportDestError(t, err)
    }
  }

  return (
    <div className="flex flex-col gap-3">
      <Row label={t(`${D}group`)}>
        {group
          ? (
            <div className="flex flex-wrap items-center gap-2">
              <span>{group.name}</span>
              {(group.state === 'report' || group.state === 'on') && <GroupStateBadge state={group.state} day={stateDay(group.state_changed_at ?? null, Date.now())} />}
              {!(group.state === 'report' || group.state === 'on') && group.mode === 'open' && (
                <span data-testid="group-mode" data-state="open"><StatusBadge tone="neutral" label={t(`${D}open`)} quiet={false} /></span>
              )}
            </div>
          )
          : <span className="text-sm text-muted-foreground">{t(`${D}no_group`)}</span>}
      </Row>
      {exemption
        ? (
          <>
            <Row label={t(`${D}exemption`)} tinted
              action={<AsyncButton variant="outline" size="sm" onClick={() => lift(exemption)}>{t(`${D}remove_exemption`)}</AsyncButton>}>
              <span title={exemption.expires_at === null ? undefined : formatMsDualTz(exemption.expires_at, panelTz)}>
                {t(`${D}exemption_line`, {
                  reason: exemption.reason, by: exemption.created_by_upn || '—',
                  ago: formatRelativeTimeShort(now - exemption.created_at, t), expiry: expiry(exemption),
                })}
              </span>
            </Row>
            <p className="flex items-center gap-1.5 rounded-md bg-muted px-2 py-1.5 text-[12.5px] text-muted-foreground">
              <IconInfo aria-hidden className="size-4 shrink-0" />{t(`${D}exempt_hint`)}
            </p>
          </>
        )
        : (
          <Row label={t(`${D}exemption`)} action={<Button variant="outline" size="sm" onClick={() => setAdding(true)}>{t(`${D}add_exemption`)}</Button>}>
            <span>{t(`${D}not_exempt`)}</span>
          </Row>
        )}
      {q.data.recent_hits && (
        <HitsSection userId={userId} days={q.data.recent_hits.days} items={q.data.recent_hits.items} available={q.data.hits_available}
          onViewAll={() => void navigate(`/admin/access-control${recordsHref({ user_id: userId, since: q.data.recent_hits!.days <= 1 ? '24h' : `${q.data.recent_hits!.days}d` })}`)}
          onNodes={() => void navigate('/admin/access-control?sheet=nodes')} />
      )}
      <UsageSection key={userId} userId={userId} available={q.data.usage_available} nodes={q.data.usage_nodes} />
      <AddExemptionDialog open={adding} onClose={() => setAdding(false)} lockedUserId={userId} />
    </div>
  )
}

/**
 * Their recent hits, one line per rule that was hit (S16). The window is what
 * the server answered (`days`, already held to the retention). Whether any
 * node of the account records is told apart from "nothing hit": an account no
 * recording node serves has no hits to show, which is not the same as none.
 */
function HitsSection({ userId, days, items, available, onViewAll, onNodes }: {
  userId: number
  days: number
  items: DestUserHit[]
  available: boolean | null
  onViewAll: () => void
  onNodes: () => void
}) {
  const { t, i18n } = useRiskT()
  const scope = useQueryScope()
  const status = useDestStatus(scope)
  const num = new Intl.NumberFormat(i18n.language)
  const nodeName = new Map((status.data?.nodes ?? []).map(n => [n.panel_id, n.panel_name]))
  const sep = t('admin:access_control.drawer.hits_list_separator')
  const name = (h: DestUserHit) => {
    if (h.source_name === null) return t(`admin:access_control.drawer.hits_deleted_${h.source.startsWith('s') ? 'source_rule' : 'policy'}`)
    return h.source.startsWith('s') ? t('admin:access_control.drawer.hits_source_rule', { name: h.source_name }) : h.source_name
  }
  return (
    <div data-testid="access-hits" data-user={userId} className="mt-2 flex flex-col gap-2">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="text-sm font-medium">{t('admin:access_control.drawer.hits_title', { days })}</h3>
        {available !== false && items.length > 0 && (
          <Button variant="outline" size="sm" onClick={onViewAll}>
            {t('admin:access_control.drawer.hits_view_all')}<IconChevronRight />
          </Button>
        )}
      </div>
      {available === false
        ? (
          <div className="flex flex-wrap items-center gap-2 rounded-lg bg-muted p-3">
            <p className="min-w-0 flex-1 text-sm">{t('admin:access_control.drawer.hits_not_recorded')}</p>
            <Button variant="outline" size="sm" onClick={onNodes}>{t('admin:access_control.drawer.hits_nodes')}</Button>
          </div>
        )
        : items.length === 0
          ? <p className="text-sm text-muted-foreground">{t('admin:access_control.drawer.hits_none', { days })}</p>
          : items.map(h => {
            const where = [...(h.top_dests ?? []), (h.panels ?? []).map(id => nodeName.get(id)).filter((n): n is string => !!n).join(sep)].filter(Boolean).join(' · ')
            return (
              <div key={`${h.source}/${h.action}`} data-testid="access-hit" className="min-w-0 rounded-lg border p-3">
                <div className="flex min-w-0 items-center gap-2">
                  {h.action === 'observe'
                    ? <span data-testid="hit-action" data-state="observe"><StatusBadge tone="info" quiet={false} label={t('admin:access_control.records.rule.would_block')} /></span>
                    : <span data-testid="hit-action" data-state="block"><StatusBadge tone="danger" label={t('admin:access_control.records.rule.blocked')} /></span>}
                  <span className={`min-w-0 flex-1 text-sm [overflow-wrap:anywhere] ${h.source_name === null ? 'italic' : ''}`}>{name(h)}</span>
                  <span className="font-semibold tabular-nums">{num.format(h.count)}</span>
                </div>
                {where && <p className="text-sm text-muted-foreground [overflow-wrap:anywhere]">{where}</p>}
              </div>
            )
          })}
    </div>
  )
}
