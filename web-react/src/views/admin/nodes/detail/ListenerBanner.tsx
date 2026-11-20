import { Link } from 'react-router'
import type { Node } from '@/api/types'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { useNodeDetail } from '../lines/useNodeDetail'
import { useNodesT } from '../shared/useNodesT'

/**
 * A tunnel line's listener is a hidden node (design 4.7): its name, address, exit and multiplier follow the line
 * that owns it. The banner says whose it is and links to the line, where those are edited.
 */
export function ListenerBanner({ node }: { node: Node }) {
  const t = useNodesT()
  const parentId = node.parent_node_id ?? 0
  const parent = useNodeDetail(parentId)
  const line = parent.data?.node.relays?.find(r => r.id === node.parent_line_id)
  const name = node.parent_node_name ?? parent.data?.node.display_name ?? `#${parentId}`
  return (
    <Alert>
      <AlertDescription className="flex flex-col gap-2">
        <span data-testid="listener-banner">
          {line?.name ? t('nodes.tunnel.listener_banner', { parent: name, line: line.name }) : t('nodes.tunnel.listener_banner_no_line', { parent: name })}
        </span>
        <Link className="w-fit underline underline-offset-2" to={`/admin/nodes/${parentId}?edit=${encodeURIComponent(node.parent_line_id ?? '')}`}>{t('nodes.detail.to_line')}</Link>
      </AlertDescription>
    </Alert>
  )
}
