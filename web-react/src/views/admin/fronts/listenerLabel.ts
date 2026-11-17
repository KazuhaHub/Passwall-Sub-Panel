type Translate = (key: string, values?: Record<string, unknown>) => string

/** The fields that say a listener is a tunnel line's hidden listener. */
export interface TunnelOwner {
  parent_node_id?: number
  parent_name?: string
  line_name?: string
}

/** A listener is named by its node, except a tunnel line's hidden listener, which is named by its line. */
export function listenerLabel(t: Translate, name: string, owner?: TunnelOwner): string {
  if (!owner?.parent_node_id) return name
  return t('admin:servers.fronts.tunnel_listener', { parent: owner.parent_name ?? `#${owner.parent_node_id}`, line: owner.line_name ?? '' })
}
