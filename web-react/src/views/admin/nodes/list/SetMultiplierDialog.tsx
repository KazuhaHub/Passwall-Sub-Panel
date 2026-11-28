import { useMemo, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { updateServerLineFactors } from '@/api/servers'
import { AsyncButton } from '@/components/common/AsyncButton'
import { Notice } from '@/components/common/Notice'
import { pushToast } from '@/components/feedback/toasts'
import { Field } from '@/components/form/Field'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { nodeKeys } from '@/query/keys'
import { useQueryScope } from '@/query/useQueryScope'
import { allSettledLimited } from '@/utils/promises'
import { factorTextToMilli, milliToFactorText } from '@/utils/trafficFactor'
import { useNodesT } from '../shared/useNodesT'
import { groupByServer, type MultiplierLine } from './multiplierModel'

/**
 * One multiplier for one or many lines, with what each line has now and will have. Used by a row's menu (one line)
 * and by the bulk bar (the selected lines). It saves one request per server and closes when all of them are done.
 * Mount only while open.
 */
export function SetMultiplierDialog({ lines, onClose, onDone }: { lines: readonly MultiplierLine[]; onClose: () => void; onDone?: () => void }) {
  const t = useNodesT()
  const scope = useQueryScope()
  const queryClient = useQueryClient()
  const [text, setText] = useState(() => (lines.length === 1 ? milliToFactorText(lines[0].old) : '1'))
  const [touched, setTouched] = useState(false)
  const [error, setError] = useState('')
  const milli = factorTextToMilli(text)
  const invalid = milli === null && touched
  const changing = useMemo(() => milli !== null && lines.some(l => l.old !== milli), [milli, lines])

  async function apply() {
    if (milli === null) return
    setError('')
    const groups = groupByServer(lines, milli)
    const results = await allSettledLimited(groups, g => updateServerLineFactors(g.serverId, g.changes))
    const failed = results.filter(r => r.status === 'rejected')
    await queryClient.invalidateQueries({ queryKey: nodeKeys.all(scope) })
    if (failed.length > 0) {
      const reason = (failed[0] as PromiseRejectedResult).reason
      const message = reason instanceof Error ? reason.message : String(reason)
      setError(failed.length < groups.length ? t('nodes.multiplier.partial', { ok: groups.length - failed.length, fail: failed.length }) + ' ' + message : t('nodes.multiplier.failed', { message }))
      return
    }
    pushToast({ message: t('nodes.multiplier.toast', { count: lines.length }), severity: 'success' })
    onDone?.()
    onClose()
  }

  return (
    <Dialog open onOpenChange={open => { if (!open) onClose() }}>
      <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{lines.length === 1 ? t('nodes.multiplier.title_one', { name: lines[0].label }) : t('nodes.multiplier.title_bulk', { count: lines.length })}</DialogTitle>
          <DialogDescription>{t('nodes.multiplier.hint')}</DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-4">
          <Field label={t('nodes.multiplier.field')} hint={t('nodes.multiplier.field_hint')} error={invalid ? t('nodes.multiplier.invalid') : undefined}>
            <Input inputMode="decimal" className="max-w-40 tabular-nums" value={text} onChange={e => setText(e.target.value)} onBlur={() => setTouched(true)} />
          </Field>
          {lines.length > 1 && (
            <table className="w-full text-sm" data-testid="multiplier-preview">
              <thead>
                <tr className="text-left text-xs text-muted-foreground">
                  <th className="pb-1 font-medium">{t('nodes.multiplier.line')}</th>
                  <th className="pb-1 text-right font-medium">{t('nodes.multiplier.old')}</th>
                  <th className="pb-1 text-right font-medium">{t('nodes.multiplier.new')}</th>
                </tr>
              </thead>
              <tbody className="block max-h-64 overflow-y-auto [&>tr]:table [&>tr]:w-full [&>tr]:table-fixed">
                {lines.map(l => (
                  <tr key={l.key} data-testid="multiplier-row">
                    <td className="truncate py-1 pr-2" title={l.label}>{l.label}</td>
                    <td className="w-16 py-1 text-right tabular-nums text-muted-foreground">×{milliToFactorText(l.old)}</td>
                    <td className="w-16 py-1 text-right tabular-nums">{milli === null ? '—' : `×${milliToFactorText(milli)}`}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          {error && <Notice tone="danger">{error}</Notice>}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>{t('common:actions.cancel')}</Button>
          <AsyncButton onClick={apply} disabled={milli === null || !changing}>{t('nodes.multiplier.apply')}</AsyncButton>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
