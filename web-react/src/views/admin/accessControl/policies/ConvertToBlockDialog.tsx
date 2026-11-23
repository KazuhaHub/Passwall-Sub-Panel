import { useMemo, useRef } from 'react'
import { useTranslation } from 'react-i18next'

import type { DestPolicy } from '@/api/accessControl'
import { KpiGrid, KpiTile } from '@/components/common/KpiTile'
import { AsyncButton } from '@/components/common/AsyncButton'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Skeleton } from '@/components/ui/skeleton'
import { useDestHits } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'

const C = 'admin:access_control.convert.'
const DAY = 86_400_000

/**
 * S4's "turn into a block" for a report-only policy: what it would have
 * blocked in the window, and why the real effect may be larger, then one
 * button. Switching report to on is a plain state change (the policy is
 * written whole, with the updated_at it was read at), so there is no second
 * confirmation after this one.
 *
 * The numbers are THIS policy's: its would-block count comes from the policy
 * list; the accounts and the top destinations from two reads of the hit
 * records filtered to its source (the response's own `summary` ignores the
 * source filter, so it cannot give "accounts involved"). When no node records
 * there is nothing to estimate from: the dialog says so, shows no 0, and does
 * not ask. A failed read leaves the button working.
 */
export default function ConvertToBlockDialog({ policy, days, onClose, onConfirm, onViewRecords }: {
  /** The report-only policy, or null (closed). */
  policy: DestPolicy | null
  /** The window of the figures (`hit_window_days`). */
  days: number
  onClose: () => void
  onConfirm: (policy: DestPolicy) => Promise<void>
  onViewRecords: (policy: DestPolicy) => void
}) {
  const { t, i18n } = useTranslation(['admin'])
  const scope = useQueryScope()
  const num = new Intl.NumberFormat(i18n.language)
  // Enforcing is the one consequential step here, so focus starts on Cancel, never on the button that does it.
  const cancelRef = useRef<HTMLButtonElement>(null)
  const id = policy?.id ?? 0
  const estimable = policy !== null && policy.would_block_recent !== null
  // One instant per opening: the reads are keyed by it, so it must not move on a re-render.
  const since = useMemo(() => Date.now() - days * DAY, [id, days]) // eslint-disable-line react-hooks/exhaustive-deps
  const base = { source: `p${id}`, action: 'observe' as const, since }
  const sites = useDestHits(scope, { ...base, group_by: 'site', page_size: 3 }, { enabled: estimable })
  const users = useDestHits(scope, { ...base, group_by: 'user', page_size: 1 }, { enabled: estimable })

  const loading = estimable && (sites.isPending || users.isPending)
  const failed = estimable && (sites.isError || users.isError)
  const would = policy?.would_block_recent ?? 0
  const top = (sites.data?.items ?? []).filter(i => i.dest).map(i => `${i.dest} ${num.format(i.count)}`).join(' · ')

  return (
    <Dialog open={policy !== null} onOpenChange={next => { if (!next) onClose() }}>
      <DialogContent showCloseButton={false} className="sm:max-w-lg" initialFocus={cancelRef}>
        <DialogHeader><DialogTitle>{t(`${C}title`, { name: policy?.name ?? '' })}</DialogTitle></DialogHeader>
        <div className="grid gap-3">
          {!estimable && <p className="text-sm text-muted-foreground">{t(`${C}no_collect`)}</p>}
          {loading && <div data-testid="convert-loading"><Skeleton className="h-16 w-full" /></div>}
          {failed && <div role="alert" className="rounded-lg border border-destructive/30 bg-destructive/10 p-2 text-sm">{t(`${C}read_failed`)}</div>}
          {estimable && !loading && !failed && (would === 0
            ? <p className="text-sm text-muted-foreground">{t(`${C}no_hits`, { days })}</p>
            : (
              <>
                <div data-testid="convert-numbers">
                  <KpiGrid className="my-0">
                    <KpiTile label={t(`${C}hits`, { days })} value={t(`${C}at_least`, { n: num.format(would) })} />
                    <KpiTile label={t(`${C}users`)} value={num.format(users.data?.total ?? 0)} />
                  </KpiGrid>
                </div>
                {top && <p data-testid="convert-top" className="text-sm [overflow-wrap:anywhere]">{t(`${C}top`, { list: top })}</p>}
              </>
            ))}
          <p className="text-sm">{t(`${C}body`)}</p>
          {policy?.counts_as_risk === true && <p className="text-sm">{t(`${C}counts_as_risk`)}</p>}
        </div>
        <DialogFooter className="sm:items-center">
          {estimable && would > 0 && (
            <Button size="sm" variant="ghost" className="sm:mr-auto" onClick={() => policy && onViewRecords(policy)}>{t(`${C}view_records`)}</Button>
          )}
          <Button ref={cancelRef} variant="outline" onClick={onClose}>{t(`${C}cancel`)}</Button>
          <AsyncButton onClick={async () => { if (policy) await onConfirm(policy) }}>{t(`${C}action`)}</AsyncButton>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
