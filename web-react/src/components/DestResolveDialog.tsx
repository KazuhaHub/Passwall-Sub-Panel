import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import type { DestNodeStatus, DestResolve } from '@/api/accessControl'
import { ModeRadioGroup } from '@/components/access/ModeRadioGroup'
import { AsyncButton } from '@/components/common/AsyncButton'
import { pushSnack } from '@/components/feedback/toasts'
import { ResolveUnavailableBadge } from '@/components/NodePolicyStatusRow'
import { Alert } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { useDirtyClose } from '@/hooks/useDirtyClose'
import { useDestStatus, useSetDestResolve } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { applyMinutes, nodeResolve } from '@/utils/accessControl'
import { discardChangesConfirm } from '@/views/admin/accessControl/confirmCopy'
import { destErrorText } from '@/views/admin/accessControl/destErrors'

const A = 'admin:access_control.dest_resolve.'

/**
 * The third row is the engine's IPIfNonMatch. It is listed so an admin who knows
 * the Xray setting finds it and reads why it is absent; it can never be chosen,
 * and the API refuses it.
 */
type Row = DestResolve | 'if_non_match'
const ROWS: Row[] = ['as_is', 'on_demand', 'if_non_match']
const CAVEATS = ['fail_open_closed', 'dial_again', 'route_only'] as const

/**
 * "How do the IP rules treat a host name?" (per native node): the one place a
 * server's hostname-matching mode is written, shared by the coverage sheet and
 * the server's access dialog. The restart sentence beside Save is the warning,
 * so there is no confirmation after it. Where a node set to on_demand runs
 * as_is anyway (too old, or an inbound that sniffs with routeOnly), the reason
 * is said here, not left for the admin to infer.
 */
export default function DestResolveDialog({ node, onClose }: {
  /** The node being set, or null (closed). */
  node: DestNodeStatus | null
  onClose: () => void
}) {
  const { t } = useTranslation(['admin'])
  const scope = useQueryScope()
  const open = node !== null
  const status = useDestStatus(scope, { enabled: open })
  const save = useSetDestResolve(scope)
  const view = node ? nodeResolve(node) : null
  const stored: DestResolve = view?.mode ?? 'as_is'
  const [mode, setMode] = useState<DestResolve>(stored)
  const [error, setError] = useState<string | null>(null)
  const dirty = open && mode !== stored

  // Seeded on each open: a node's mode read while the dialog is open never overwrites a choice in progress.
  useEffect(() => { if (open) { setMode(stored); setError(null) } }, [open, node?.panel_id]) // eslint-disable-line react-hooks/exhaustive-deps

  const guard = useDirtyClose(dirty, discardChangesConfirm(t))
  const minutes = applyMinutes(status.data)

  const submit = async () => {
    if (!node) return
    setError(null)
    try {
      await save.mutateAsync({ serverId: node.panel_id, resolve: mode })
      pushSnack(minutes === null
        ? t(`${A}saved_unknown`, { node: node.panel_name })
        : t(`${A}saved`, { node: node.panel_name, count: minutes }), 'success')
      onClose()
    } catch (err) {
      setError(destErrorText(t, err))
    }
  }

  return (
    <Dialog open={open} onOpenChange={next => { if (!next) guard(onClose) }}>
      <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t(`${A}title`)}</DialogTitle>
          {node && <p className="text-sm text-muted-foreground">{node.panel_name}</p>}
        </DialogHeader>
        <div className="grid gap-3">
          {view?.unavailable && <div><ResolveUnavailableBadge reason={view.unavailable} /></div>}
          {error && <Alert variant="destructive">{error}</Alert>}
          <ModeRadioGroup<Row>
            value={mode} aria-label={t(`${A}label`)}
            onChange={v => { if (v === 'as_is' || v === 'on_demand') setMode(v) }}
            options={ROWS.map(value => ({ value, disabled: value === 'if_non_match' || save.isPending, children: t(`${A}option.${value}`) }))} />
          <ul data-testid="resolve-cost" className="grid list-disc gap-1 pl-5 text-sm text-muted-foreground">
            {(['xray', 'singbox'] as const).map(e => <li key={e}>{t(`${A}cost.${e}`)}</li>)}
          </ul>
          <ul data-testid="resolve-caveats" className="grid list-disc gap-1 pl-5 text-sm text-muted-foreground">
            {CAVEATS.map(c => <li key={c}>{t(`${A}caveat.${c}`)}</li>)}
          </ul>
        </div>
        <p className="text-xs text-muted-foreground">{t(`${A}restart`)}</p>
        <DialogFooter>
          <Button variant="outline" onClick={() => guard(onClose)}>{t(`${A}cancel`)}</Button>
          <AsyncButton disabled={!dirty} onClick={submit}>{t(`${A}save`)}</AsyncButton>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
