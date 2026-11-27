import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { destErrorOf, type GeositeRejection, type GeositeUpdateResult } from '@/api/accessControl'
import { AccessBadge } from '@/components/access/AccessBadge'
import { AsyncButton } from '@/components/common/AsyncButton'
import { FieldHint } from '@/components/common/HelpTip'
import { ErrorState } from '@/components/feedback/ErrorState'
import { pushToast } from '@/components/feedback/toasts'
import { SegmentedControl } from '@/components/form/SegmentedControl'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { IconError } from '@/lib/icons'
import { useGeositeStatus, useRollbackGeosite, useUpdateGeosite } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { destErrorText } from './destErrors'

const R = 'admin:access_control.rule_data.'

type Op = 'update' | 'rollback'

/** Why a release cannot be applied, in words; the list name rides along. */
function rejectionText(t: (k: string, o?: Record<string, unknown>) => string, r: GeositeRejection): string {
  const known = ['broad_entry', 'dest_list_too_large', 'category_gone', 'policy_over_limit'] as const
  const key = (known as readonly string[]).includes(r.reason) ? r.reason : 'other'
  return t(`${R}rejection.${key}`, { list: r.list ?? '', entry: r.entry ?? '', used: r.used ?? '', limit: r.limit ?? '' })
}

/**
 * THE RULE-DATA VERSION: which v2fly release the community categories come
 * from, how old it is, what the upstream offers, and the update / roll back
 * pair. An update is always previewed first (a dry run downloads and verifies a
 * release and changes nothing) and applied by naming the tag that was reviewed.
 * The auto / manual mode is the settings sheet's own draft (it is saved with the other
 * settings), so it comes in as props.
 */
export default function RuleDataPanel({ mode, onModeChange, disabled }: {
  mode: 'manual' | 'auto'
  onModeChange: (m: 'manual' | 'auto') => void
  disabled?: boolean
}) {
  const { t, i18n } = useTranslation(['admin'])
  const scope = useQueryScope()
  const status = useGeositeStatus(scope)
  const update = useUpdateGeosite(scope)
  const rollback = useRollbackGeosite(scope)
  const [review, setReview] = useState<{ op: Op; result: GeositeUpdateResult } | null>(null)
  const [error, setError] = useState('')

  const date = (iso?: string) => (iso ? new Date(iso).toLocaleDateString(i18n.language) : '')

  if (status.isPending) return <Skeleton className="h-24 w-full" />
  // A failed REFRESH keeps the data on screen (and the review dialog with its message); only a read that never succeeded replaces it.
  if (!status.data) {
    return <ErrorState compact title={t(`${R}read_failed`)} retry={() => void status.refetch()} />
  }
  const s = status.data
  const busy = update.isPending || rollback.isPending

  const preview = async (op: Op) => {
    setError('')
    try {
      const result = op === 'update' ? await update.mutateAsync({ dryRun: true }) : await rollback.mutateAsync({ dryRun: true })
      setReview({ op, result })
    } catch (err) {
      setError(destErrorText(t, err))
    }
  }

  const apply = async () => {
    if (!review) return
    try {
      // The apply names the release the dry run reviewed, so one that lands in between changes nothing.
      if (review.op === 'update') await update.mutateAsync({ tag: review.result.to.tag })
      else {
        // The rollback API takes no tag: it goes to whatever is "previous" now. So the reviewed
        // version is re-checked first, and the apply is abandoned if it moved.
        const fresh = await status.refetch()
        if (fresh.isError || fresh.error) {
          // Without a fresh read the reviewed version cannot be confirmed: do not apply.
          setReview(null)
          setError(t(`${R}rollback_recheck_failed`))
          return
        }
        if (fresh.data?.previous?.tag !== review.result.to.tag) {
          setReview(null)
          setError(t(`${R}rollback_changed`))
          return
        }
        await rollback.mutateAsync({})
      }
      pushToast({ message: t(`${R}applied_snack`, { tag: review.result.to.tag }), severity: 'success' })
      setReview(null)
    } catch (err) {
      const e = destErrorOf(err)
      const rejection = e?.code === 'dest_geosite_update_rejected' ? e.body.rejection as GeositeRejection | undefined : undefined
      setReview(null)
      setError(rejection ? rejectionText(t, rejection) : e?.code === 'dest_geosite_update_rejected' ? t(`${R}rejection.refused`) : destErrorText(t, err))
    }
  }

  const rej = review?.result.rejection
  const diff = review?.result
  const age = s.current_age_days

  return (
    <div data-testid="rule-data-panel" className="grid gap-1.5 text-sm">
      {!s.current ? (
        <p className="text-muted-foreground">{t(`${R}none`)}</p>
      ) : (
        <p>
          {t(`${R}current`, { tag: s.current.tag })}
          {s.current_released_at ? ` · ${t(`${R}released`, { date: date(s.current_released_at) })}` : ''}
          {age !== undefined ? ` · ${t(`${R}age`, { count: age })}` : ''}
        </p>
      )}
      <p>
        {s.latest.tag ? t(`${R}latest`, { tag: s.latest.tag }) : t(`${R}latest_unknown`)}
        {s.update_available && s.behind_days !== undefined ? ` · ${t(`${R}behind`, { count: s.behind_days })}` : ''}
      </p>
      {s.update_available && <div><AccessBadge kind="attention" label={t(`${R}update_available`)} /></div>}
      {s.latest.error && <p className="text-muted-foreground">{t(`${R}latest_error`)}</p>}
      {(s.last_update_error || s.data_error) && (
        <div>
          <FieldHint summary={s.data_error ? t(`${R}data_error.summary`) : t(`${R}last_update_error.summary`)}
            detail={s.data_error ? t(`${R}data_error.detail`) : t(`${R}last_update_error.detail`)} />
        </div>
      )}

      <div className="mt-1.5 flex flex-wrap gap-2">
        <AsyncButton variant="outline" size="sm" disabled={disabled || busy} onClick={() => preview('update')}>{t(`${R}check_update`)}</AsyncButton>
        {s.previous && (
          <AsyncButton variant="outline" size="sm" disabled={disabled || busy} onClick={() => preview('rollback')}>{t(`${R}rollback`, { tag: s.previous.tag })}</AsyncButton>
        )}
      </div>
      {error && (
        <Alert variant="destructive" className="mt-1">
          <IconError aria-hidden />
          <AlertDescription className="flex items-start justify-between gap-2">
            <span>{error}</span>
            <Button variant="outline" size="xs" onClick={() => setError('')}>{t('common:actions.close')}</Button>
          </AlertDescription>
        </Alert>
      )}

      <div className="mt-3 grid gap-1.5">
        <p id="rules-mode-label" className="font-semibold">{t(`${R}mode.title`)}</p>
        <SegmentedControl<'manual' | 'auto'> aria-labelledby="rules-mode-label" value={mode} disabled={disabled}
          onChange={onModeChange} className="w-fit"
          options={[{ value: 'manual', label: t(`${R}mode.manual`) }, { value: 'auto', label: t(`${R}mode.auto`) }]} />
        <p className="text-muted-foreground">{t(`${R}mode.${mode}_hint`)}</p>
      </div>

      <Dialog open={review !== null} onOpenChange={next => { if (!next && !busy) setReview(null) }}>
        <DialogContent className="sm:max-w-xl">
          <DialogHeader>
            <DialogTitle>{review?.op === 'rollback' ? t(`${R}review.title_rollback`) : t(`${R}review.title_update`)}</DialogTitle>
            <DialogDescription>
              {diff && (diff.from ? t(`${R}review.from_to`, { from: diff.from.tag, to: diff.to.tag }) : t(`${R}review.to`, { to: diff.to.tag }))}
            </DialogDescription>
          </DialogHeader>
          {diff && (
            <div className="grid gap-3">
              {rej && <Alert variant="destructive"><IconError aria-hidden /><AlertDescription>{rejectionText(t, rej)}</AlertDescription></Alert>}
              {diff.unchanged ? (
                <p className="text-sm text-muted-foreground">{t(`${R}review.unchanged`)}</p>
              ) : (
                <Table className="tabular-nums">
                  <TableHeader>
                    <TableRow>
                      <TableHead>{t(`${R}review.category`)}</TableHead>
                      <TableHead className="text-right">{t(`${R}review.entries`)}</TableHead>
                      <TableHead className="text-right">{t(`${R}review.change`)}</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {diff.categories.map(c => (
                      <TableRow key={c.name}>
                        <TableCell className="break-words whitespace-normal">
                          {c.name}
                          {c.in_use && <span className="ml-2"><AccessBadge kind="measuring" label={t(`${R}review.in_use`)} /></span>}
                        </TableCell>
                        <TableCell className="text-right">{c.old} → {c.new}</TableCell>
                        <TableCell className="text-right">{`+${c.added} −${c.removed}`}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              )}
              {!diff.unchanged && !rej && (
                <p className="text-sm text-muted-foreground">
                  {diff.lists_changed > 0 ? t(`${R}review.restart`) : t(`${R}review.no_restart`)}
                </p>
              )}
            </div>
          )}
          <DialogFooter>
            <Button variant="outline" onClick={() => setReview(null)} disabled={busy}>{t(`${R}review.cancel`)}</Button>
            <AsyncButton disabled={!!rej || !!diff?.unchanged} onClick={apply}>
              {review?.op === 'rollback' ? t(`${R}review.apply_rollback`) : t(`${R}review.apply_update`)}
            </AsyncButton>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
