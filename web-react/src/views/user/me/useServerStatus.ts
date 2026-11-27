import { useEffect, useRef, useState } from 'react'
import { getMyServerStatus, type MyNodeStatus } from '@/api/me'
import { hasCustomFactor } from '@/utils/trafficFactor'

/**
 * The caller's own servers and their availability. It is read here, not inside the status tab, because the overview's
 * usage card needs to know whether any line has a non-default multiplier (it then says the used figure is "charged").
 * It is read once on load, and again each time the status tab is opened, so that tab never shows a status older than
 * the visit to it. A failed read is an empty list, not an error page: the tab is auxiliary.
 */
export function useServerStatus(statusTabOpen: boolean) {
  const [nodes, setNodes] = useState<MyNodeStatus[] | null>(null)
  const [loading, setLoading] = useState(true)
  const read = useRef(false)
  useEffect(() => {
    if (read.current && !statusTabOpen) return
    let live = true
    // Marked read only once an answer has landed: in development React runs the effect, cleans it up and runs it again, and
    // marking it up front would let the second run skip the read whose answer the cleanup had already discarded.
    getMyServerStatus()
      .then(list => { if (live) setNodes(list) })
      .catch(() => { if (live) setNodes([]) })
      .finally(() => { if (live) { read.current = true; setLoading(false) } })
    return () => { live = false }
  }, [statusTabOpen])
  const charged = (nodes ?? []).some(n =>
    hasCustomFactor(n.traffic_factor_milli) || (n.relay_statuses ?? []).some(r => hasCustomFactor(r.traffic_factor_milli)))
  return { nodes, loading, charged }
}
