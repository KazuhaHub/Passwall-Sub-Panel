import { useCallback, useRef, useState, type ReactNode } from 'react'
import { switchLineMode } from '@/api/nodes'
import { pushToast } from '@/components/feedback/toasts'
import { useNodesT } from '../shared/useNodesT'
import { DeleteLineDialog, type DeleteTarget } from './DeleteLineDialog'
import { saveLineChange } from './lineSave'
import { usePlanPresenter } from './PlanDialog'
import { useApplyNode } from './useNodeDetail'

/**
 * Enable, disable and delete lines, for every place that offers them (the list's row menu, the sheet, the bulk bar
 * and the node page's cards). They go through the same save path as the editor, so a tunnel line's plan is asked
 * in this page's own dialog. `dialogs` must be rendered by the caller.
 */
export function useLineActions() {
  const t = useNodesT()
  const apply = useApplyNode()
  const { present, dialog } = usePlanPresenter()
  const [target, setTarget] = useState<DeleteTarget | null>(null)
  const live = useRef({ t, apply, present })
  live.current = { t, apply, present }

  /** One write per node, however many of its lines. A toast offers to put it back (`quiet` leaves the toast to a caller that reports a whole batch). */
  const toggle = useCallback(async function run(nodeId: number, lineIds: readonly string[], enabled: boolean, opts?: { quiet?: boolean }): Promise<boolean> {
    const { t, apply, present } = live.current
    try {
      const { saved } = await saveLineChange(nodeId, { kind: 'toggle', lineIds, enabled }, { present })
      apply(saved)
      if (opts?.quiet) return true
      pushToast({
        message: t(enabled ? 'nodes.line.toast.enabled' : 'nodes.line.toast.disabled'), severity: 'success',
        action: { label: t('nodes.toast.undo'), onClick: () => void run(nodeId, lineIds, !enabled) },
      })
      return true
    } catch { return false /* The request layer already surfaced the failure. */ }
  }, [])

  /** A saved line between a direct forward and an encrypted tunnel; the plan of the listeners it opens or closes is asked in this page's dialog. */
  const switchMode = useCallback(async (nodeId: number, lineId: string, next: 'forward' | 'tunnel') => {
    const { t, apply, present } = live.current
    try {
      apply(await switchLineMode(nodeId, lineId, next, 0, { present }))
      pushToast({ message: t(next === 'tunnel' ? 'nodes.tunnel.switched_tunnel' : 'nodes.tunnel.switched_forward'), severity: 'success' })
    } catch { /* The request layer already surfaced a failure; a declined plan changed nothing. */ }
  }, [])

  const askDelete = useCallback((t: DeleteTarget) => setTarget(t), [])
  const dialogs: ReactNode = (
    <>
      {dialog}
      {target && (
        <DeleteLineDialog target={target} onClose={() => setTarget(null)}
          onDisable={() => { const row = target.row; setTarget(null); void toggle(row.node_id ?? 0, [row.line_id ?? ''], false) }} />
      )}
    </>
  )
  return { toggle, switchMode, askDelete, dialogs }
}
