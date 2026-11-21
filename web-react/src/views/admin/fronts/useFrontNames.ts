import { useCallback, useMemo } from 'react'
import type { FrontOptions, FrontTarget } from '@/api/fronts'
import { useAdminT } from '../useAdminT'
import { listenerLabel } from './listenerLabel'

/**
 * How a shared port names what it touches, from the options the server offers (listeners, relay lines, tunnels).
 * While the options have not loaded, or for something they no longer list, a name falls back to its id.
 */
export function useFrontNames(options: FrontOptions | undefined) {
  const t = useAdminT()
  const nodeName = useCallback((id?: number) => {
    const o = options?.listeners.find(l => l.node_id === id)
    return o ? listenerLabel(t, o.name, o) : `#${id ?? 0}`
  }, [options, t])
  const isTunnel = useCallback((id?: number) => !!options?.listeners.find(l => l.node_id === id)?.parent_node_id, [options])
  /** What a route or the default hands connections to, in the admin's words. */
  const targetText = useCallback((target: FrontTarget): string => {
    switch (target.kind) {
      case 'listener': return isTunnel(target.node_id) ? nodeName(target.node_id) : t('admin:servers.fronts.target.listener', { name: nodeName(target.node_id) })
      case 'forward': return t('admin:servers.fronts.target.forward_short', { node: nodeName(target.node_id), line: target.line_id ?? '' })
      case 'link': return t('admin:servers.fronts.target.link_short', { link: target.link_id ?? 0 })
      default: return t('admin:servers.fronts.ports.to_external', { host: target.host ?? '', port: target.port ?? 0 })
    }
  }, [isTunnel, nodeName, t])
  return useMemo(() => ({ nodeName, isTunnel, targetText }), [nodeName, isTunnel, targetText])
}
