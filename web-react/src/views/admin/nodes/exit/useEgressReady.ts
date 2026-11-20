import { useEffect } from 'react'
import { getNode } from '@/api/nodes'
import type { Node } from '@/api/types'
import { EGRESS_READY_POLL_MS } from '../inbound/inboundModel'
import { useApplyNode } from '../lines/useNodeDetail'

/**
 * While a node's exit tunnel is not yet running on both servers, asks again every few seconds so "waiting" turns to
 * "ready" without a reload. The answer goes into the shared caches (`apply`), so the node, its lines in the list
 * and their publication state all learn it; nothing here touches what the person is typing in a form.
 *
 * Quiet reads: a failed poll is not worth a toast, the next one tries again. An answer for another exit than the
 * one asked about is ignored: the exit was changed in the meantime.
 */
export function useEgressReady(node: Node) {
  const apply = useApplyNode()
  const exit = node.egress_panel_id ?? 0
  const ready = !!node.egress_ready
  const id = node.id
  useEffect(() => {
    if (exit <= 0 || ready) return
    const controller = new AbortController()
    let inFlight = false
    const timer = setInterval(() => {
      if (inFlight) return
      inFlight = true
      getNode(id, { signal: controller.signal, quiet: true })
        .then(({ node: fresh }) => { if (!controller.signal.aborted && fresh.egress_ready && fresh.egress_panel_id === exit) apply(fresh) })
        .catch(() => {})
        .finally(() => { inFlight = false })
    }, EGRESS_READY_POLL_MS)
    return () => { controller.abort(); clearInterval(timer) }
  }, [id, exit, ready, apply])
}
