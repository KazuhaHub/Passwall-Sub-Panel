import { DetailDialog } from '@/components/data/DetailDialog'
import { useNodesT } from '../shared/useNodesT'
import { LineEditor } from './LineEditor'
import { useGuardedEditor } from './useGuardedEditor'

/**
 * The line editor in a dialog of its own, for the node page (the list has its own dialog, which switches between the
 * view and the editor in place). Closing it with unsaved edits asks first; so does leaving the page.
 * `param` is the address parameter that names the open line.
 */
export function LineEditDialog({ nodeId, lineId, title, param, onClose, onSaved }: {
  nodeId: number
  /** The line's id, 'direct', or null for a new line. */
  lineId: string | null
  title: string
  param: string
  onClose: () => void
  onSaved: (result: { addedId?: string; isNew: boolean }) => void
}) {
  const t = useNodesT()
  const guard = useGuardedEditor(param)
  return (
    <DetailDialog open size="lg" onClose={() => guard.close(onClose)} title={lineId === null ? t('nodes.action.add_line') : title}>
      <LineEditor nodeId={nodeId} lineId={lineId} onDirtyChange={guard.setDirty} onSaved={r => guard.leave(() => onSaved(r))} />
    </DetailDialog>
  )
}
