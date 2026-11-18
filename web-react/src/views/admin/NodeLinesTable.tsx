import { useState } from 'react'
import { Button, Stack, Table, TableBody, TableCell, TableContainer, TableHead, TableRow, TextField } from '@mui/material'
import { useTranslation } from 'react-i18next'
import type { Node } from '@/api/types'
import type { Server } from '@/api/servers'
import { DEFAULT_TRAFFIC_FACTOR_MILLI, milliToFactorText } from '@/utils/trafficFactor'

interface Props {
  nodes: Node[]
  servers: Server[]
  canEdit: boolean
  onEdit: (node: Node, lineId?: string) => void
  onAdd: (node: Node) => void
}

export default function NodeLinesTable({ nodes, servers, canEdit, onEdit, onAdd }: Props) {
  const { t } = useTranslation(['admin', 'common'])
  const [query, setQuery] = useState('')
  const serverName = (id: number | undefined, fallback = '—') => servers.find(s => s.id === id)?.name ?? fallback
  const items = nodes.filter(n => n.kind !== 'separator' && !n.parent_node_id).flatMap(node => {
    const disabled = !node.enabled
    const landing = serverName(node.panel_id, node.panel_name)
    const directStatus = disabled || node.hide_direct ? 'disabled'
      : node.egress_withheld ? 'issue' : node.egress_panel_id && !node.egress_ready ? 'pending' : 'published'
    const direct = {
      key: `${node.id}:direct`, id: 'direct', node, kind: 'landing', entry: landing,
      name: node.display_name, status: directStatus, address: node.server_address, port: node.desired_port,
      factor: node.traffic_factor_milli, exit: serverName(node.egress_panel_id, landing),
    }
    return [direct, ...(node.relays ?? []).map((line, i) => {
      const managed = !!line.entry_panel_id
      const kind = line.mode === 'cdn' ? 'cdn' : line.mode === 'tunnel' ? 'tunnel' : managed ? 'forward' : 'external'
      const status = disabled || !line.enabled ? 'disabled'
        : line.probe_withheld || line.tunnel?.egress_withheld || line.cdn_issues?.some(issue => issue.warn) ? 'issue'
        : managed && !line.running && !line.managed_ready ? 'pending'
        : kind !== 'tunnel' && node.egress_withheld ? 'issue'
        : kind !== 'tunnel' && node.egress_panel_id && !node.egress_ready ? 'pending' : 'published'
      return {
        key: `${node.id}:${line.id ?? i}`, id: line.id, node, kind, entry: serverName(line.entry_panel_id, line.domain || line.address),
        name: line.name || line.domain || line.address, status, address: line.address, port: line.tunnel?.port || line.port || node.desired_port,
        factor: line.traffic_factor_milli, exit: kind === 'tunnel' ? landing : direct.exit,
      }
    })]
  })
  const search = query.trim().toLowerCase()
  const rows = items.filter(r => [r.node.display_name, r.name, r.entry, r.exit, r.address, String(r.port ?? '')]
    .some(value => value.toLowerCase().includes(search)))

  return (
    <Stack spacing={1.5} sx={{ p: 2 }}>
      <TextField size="small" sx={{ maxWidth: 360 }} label={t('admin:nodes.lines.filter')} value={query} onChange={e => setQuery(e.target.value)} />
      <TableContainer>
        <Table size="small" aria-label={t('admin:nodes.lines.title')}>
          <TableHead><TableRow>
            {['node', 'line', 'entry', 'exit', 'status', 'factor'].map(field => <TableCell key={field}>{t(`admin:nodes.lines.${field}`)}</TableCell>)}
            {canEdit && <TableCell />}
          </TableRow></TableHead>
          <TableBody>
            {rows.map(r => (
              <TableRow key={r.key}>
                <TableCell>{r.node.display_name}</TableCell>
                <TableCell>{r.name}<br />{t(`admin:nodes.setup.kind.${r.kind}`)}</TableCell>
                <TableCell>{r.entry}<br />{r.address}{r.port ? ` · ${r.port}` : ''}</TableCell>
                <TableCell>{r.exit}</TableCell>
                <TableCell>{t(`admin:nodes.lines.${r.status}`)}</TableCell>
                <TableCell>×{milliToFactorText(r.factor ?? DEFAULT_TRAFFIC_FACTOR_MILLI)}</TableCell>
                {canEdit && <TableCell align="right" sx={{ whiteSpace: 'nowrap' }}>
                  <Button size="small" onClick={() => onEdit(r.node, r.id)}>{t('common:actions.edit')}</Button>
                  {r.id === 'direct' && <Button size="small" onClick={() => onAdd(r.node)}>{t('admin:nodes.relay.add')}</Button>}
                </TableCell>}
              </TableRow>
            ))}
            {rows.length === 0 && <TableRow><TableCell colSpan={canEdit ? 7 : 6}>{t('admin:nodes.lines.empty')}</TableCell></TableRow>}
          </TableBody>
        </Table>
      </TableContainer>
    </Stack>
  )
}
