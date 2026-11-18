import { Button, Dialog, DialogActions, DialogContent, DialogTitle, Typography } from '@mui/material'
import { useTranslation } from 'react-i18next'

/** A node that keeps a server from being deleted. A tunnel line's hidden listener names its line and landing. */
export interface BlockingNode {
  node_id: number
  node: string
  tunnel_line_of?: string
  tunnel_line?: string
  tunnel_line_of_node_id?: number
}

/** Why a server cannot be deleted: the nodes still on it, tunnel listeners named by their line. */
export default function ServerHasNodesDialog({ nodes, onClose }: { nodes: BlockingNode[] | null; onClose: () => void }) {
  const { t } = useTranslation(['admin', 'common'])
  return (
    <Dialog open={nodes !== null} onClose={onClose} fullWidth maxWidth="xs">
      <DialogTitle>{t('admin:servers.confirm.has_nodes_title')}</DialogTitle>
      <DialogContent>
        <Typography variant="body2" sx={{ mb: 1 }}>{t('admin:servers.confirm.has_nodes_text')}</Typography>
        <ul data-testid="blocking-nodes">
          {(nodes ?? []).map(n => (
            <li key={n.node_id}>
              {n.tunnel_line_of
                ? t('admin:servers.confirm.has_nodes_tunnel_item', { landing: n.tunnel_line_of, line: n.tunnel_line ?? '' })
                : t('admin:servers.confirm.has_nodes_item', { node: n.node })}
            </li>
          ))}
        </ul>
      </DialogContent>
      <DialogActions><Button onClick={onClose}>{t('common:actions.close')}</Button></DialogActions>
    </Dialog>
  )
}
