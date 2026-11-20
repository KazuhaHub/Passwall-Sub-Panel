import { deleteNode, detachNode } from '@/api/nodes'
import type { TunnelPlanPresenter } from '@/utils/tunnelConfirm'
import type { DeleteMode } from './impactModel'

/**
 * Sends the delete (or the remove-from-panel) of one node. The server answers 409 with a plan when the node's
 * tunnel listeners would close; `present` decides whether the admin has agreed to exactly that plan (see
 * planCovered), and a decline throws the refusal so nothing is deleted.
 */
export function runRemoval(mode: DeleteMode, nodeId: number, present: TunnelPlanPresenter, signal?: AbortSignal): Promise<void> {
  return (mode === 'delete' ? deleteNode : detachNode)(nodeId, { present, signal })
}
