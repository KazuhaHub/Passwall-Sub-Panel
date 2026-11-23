import { useMemo, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { updateServerLineFactors, type LineFactorChange, type Server } from '@/api/servers'
import { ErrorState } from '@/components/feedback/ErrorState'
import { EmptyState } from '@/components/feedback/EmptyState'
import { pushToast } from '@/components/feedback/toasts'
import { AsyncButton } from '@/components/common/AsyncButton'
import { Field } from '@/components/form/Field'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { nodeKeys } from '@/query/keys'
import { useNodesList } from '@/query/nodes'
import { useQueryScope } from '@/query/useQueryScope'
import { DEFAULT_TRAFFIC_FACTOR_MILLI, factorTextToMilli, milliToFactorText } from '@/utils/trafficFactor'
import { DialogNotice } from './DialogNotice'

const NODES_PARAMS = { page: 1, page_size: 500 } as const

interface LineRow { key: string; node_id: number; line_id: string; label: string; milli: number }

/**
 * Bulk traffic multiplier editor for every line of the nodes on one server: tick lines, give one multiplier, apply.
 * It is an action on a selection, not a draft of values, so it has no Save bar and nothing to lose by closing; the
 * multiplier is checked when the box is left, and Apply stays off until it is valid and a line is ticked.
 * Mount only while open: the node list is fetched on mount.
 */
export function LineMultipliersDialog({ server, onClose }: { server: Server; onClose: () => void }) {
  const { t } = useTranslation(['admin', 'common'])
  const scope = useQueryScope()
  const queryClient = useQueryClient()
  const nodes = useNodesList(scope, NODES_PARAMS)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [text, setText] = useState('1')
  const [touched, setTouched] = useState(false)
  const [applyError, setApplyError] = useState('')

  const rows = useMemo<LineRow[]>(() => {
    const out: LineRow[] = []
    for (const n of nodes.data?.items ?? []) {
      if (n.panel_id !== server.id || (n as { kind?: string }).kind === 'separator') continue
      out.push({ key: `${n.id}:direct`, node_id: n.id, line_id: 'direct', label: n.display_name, milli: n.traffic_factor_milli ?? DEFAULT_TRAFFIC_FACTOR_MILLI })
      for (const r of n.relays ?? []) {
        if (!r.id) continue
        out.push({ key: `${n.id}:${r.id}`, node_id: n.id, line_id: r.id, label: `${n.display_name} / ${r.name || (r.mode === 'cdn' && r.domain) || r.address}`, milli: r.traffic_factor_milli ?? DEFAULT_TRAFFIC_FACTOR_MILLI })
      }
    }
    return out
  }, [server, nodes.data])

  const milli = factorTextToMilli(text)
  const selectedRows = rows.filter(r => selected.has(r.key))
  const allSelected = rows.length > 0 && rows.every(r => selected.has(r.key))
  // An invalid value is said when the box is left, and also as soon as it is the reason Apply is off (a line is ticked).
  const factorError = milli === null && (touched || selected.size > 0) ? t('admin:servers.line_factors.invalid') : undefined

  function toggle(key: string, on: boolean) {
    setSelected(prev => { const next = new Set(prev); if (on) next.add(key); else next.delete(key); return next })
  }

  async function apply() {
    if (milli === null) return
    const lines: LineFactorChange[] = selectedRows
      .map(r => ({ node_id: r.node_id, line_id: r.line_id, traffic_factor_milli: milli }))
    if (lines.length === 0) return
    setApplyError('')
    try {
      const res = await updateServerLineFactors(server.id, lines)
      await queryClient.invalidateQueries({ queryKey: nodeKeys.all(scope) })
      pushToast({ message: t('admin:servers.line_factors.updated', { count: res.changed }), severity: 'success' })
      setSelected(new Set())
    } catch (err) {
      setApplyError(t('admin:servers.line_factors.apply_failed', { message: err instanceof Error ? err.message : String(err) }))
    }
  }

  return (
    <Dialog open onOpenChange={open => { if (!open) onClose() }}>
      <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t('admin:servers.line_factors.title', { name: server.name })}</DialogTitle>
          <DialogDescription>{t('admin:servers.line_factors.hint')}</DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-4">
          {nodes.isPending ? (
            <p role="status" className="py-6 text-center text-muted-foreground">{t('admin:servers.line_factors.loading')}</p>
          ) : nodes.isError ? (
            <ErrorState compact title={t('admin:servers.line_factors.load_failed')} reason={nodes.error} retry={() => nodes.refetch()} />
          ) : rows.length === 0 ? (
            <EmptyState compact title={t('admin:servers.line_factors.empty')} />
          ) : (
            <div className="flex flex-col gap-1.5">
              <label className="flex items-center gap-2 border-b pb-2 text-sm font-medium">
                <Checkbox checked={allSelected} indeterminate={!allSelected && selected.size > 0}
                  onCheckedChange={c => setSelected(c ? new Set(rows.map(r => r.key)) : new Set())} />
                <span>{t('admin:servers.line_factors.select_all')}</span>
              </label>
              <ul className="m-0 flex max-h-64 list-none flex-col overflow-y-auto p-0">
                {rows.map(r => (
                  <li key={r.key}>
                    <label className="flex min-h-9 items-center gap-2 rounded-md px-1 text-sm hover:bg-muted/60">
                      <Checkbox checked={selected.has(r.key)} onCheckedChange={c => toggle(r.key, c === true)} />
                      <span className="min-w-0 flex-1 truncate" title={r.label}>{r.label}</span>
                      <span className="shrink-0 text-xs tabular-nums text-muted-foreground">×{milliToFactorText(r.milli)}</span>
                    </label>
                  </li>
                ))}
              </ul>
            </div>
          )}
          {applyError && <DialogNotice tone="danger">{applyError}</DialogNotice>}
          <Field label={t('admin:servers.line_factors.field')} hint={t('admin:servers.line_factors.field_hint')} error={factorError}>
            <Input inputMode="decimal" value={text} onChange={e => setText(e.target.value)} onBlur={() => setTouched(true)} />
          </Field>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>{t('common:actions.cancel')}</Button>
          <AsyncButton onClick={apply} disabled={milli === null || selectedRows.length === 0}>
            {t('admin:servers.line_factors.apply')}
          </AsyncButton>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
