import { useState } from 'react'
import {
  Alert, Box, Button, Chip, CircularProgress, Dialog, DialogActions, DialogContent, DialogTitle, IconButton, Stack, Tab, Tabs,
  Tooltip, Typography,
} from '@mui/material'
import DeleteIcon from '@mui/icons-material/Delete'
import EditIcon from '@mui/icons-material/Edit'
import { useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { deleteFront, frontErrorOf, frontErrorWording, type Front, type FrontOptions } from '@/api/fronts'
import type { Server } from '@/api/servers'
import InfoTip from '@/components/InfoTip'
import { pushSnack } from '@/components/SnackbarHost'
import { confirm } from '@/components/ConfirmHost'
import { useFrontOptions, useFronts } from '@/query/fronts'
import { frontKeys } from '@/query/keys'
import { useQueryScope } from '@/query/useQueryScope'
import FrontEditor from './FrontEditor'
import { frontErrorText } from './frontErrors'
import PortUsageTable, { behindText } from './PortUsageTable'
import { listenerLabel } from './listenerLabel'

/**
 * The shared ports (SNI fronts) of one Passwall Node server, and the view of
 * every port the server uses. Mount only while open: it reads on mount.
 */
export default function SharedPortsDialog({ server, onClose }: { server: Server; onClose: () => void }) {
  const { t } = useTranslation(['admin', 'common'])
  const scope = useQueryScope()
  const queryClient = useQueryClient()
  const fronts = useFronts(scope, server.id)
  const options = useFrontOptions(scope, server.id)
  const [tab, setTab] = useState<'fronts' | 'ports'>('fronts')
  const [editing, setEditing] = useState<{ front: Front | null } | null>(null)

  const view = fronts.data
  const supported = view?.support.supported ?? false
  const full = view ? view.fronts.length >= view.max : false
  const optionData: FrontOptions | undefined = options.data

  async function remove(front: Front) {
    const ok = await confirm({
      title: t('admin:servers.fronts.delete_title', { port: front.port }),
      message: t('admin:servers.fronts.delete_text'),
      destructive: true,
    })
    if (!ok) return
    try {
      try {
        await deleteFront(server.id, front.id)
      } catch (err) {
        // A front with listeners or lines behind it asks first, naming them.
        const e = frontErrorOf(err)
        if (!e || e.status !== 409 || e.code !== 'confirm_required') throw err
        const agreed = await confirm({
          title: t('admin:servers.fronts.delete_title', { port: front.port }),
          message: t(frontErrorWording(e).key, frontErrorWording(e).values) as string,
          destructive: true,
        })
        if (!agreed) return
        await deleteFront(server.id, front.id, true)
      }
      await queryClient.invalidateQueries({ queryKey: frontKeys.all(scope) })
      pushSnack(t('admin:servers.fronts.deleted'), 'success')
    } catch (err) {
      pushSnack(frontErrorText(t, err), 'error')
    }
  }

  const nodeName = (id?: number) => {
    const o = optionData?.listeners.find(l => l.node_id === id)
    return o ? listenerLabel(t, o.name, o) : `#${id ?? 0}`
  }
  const isTunnelListener = (id?: number) => !!optionData?.listeners.find(l => l.node_id === id)?.parent_node_id

  return (
    <Dialog open onClose={onClose} fullWidth maxWidth="md">
      <DialogTitle sx={{ display: 'flex', alignItems: 'center' }}>
        {t('admin:servers.fronts.title', { name: server.name })}
        <InfoTip text={t('admin:servers.fronts.help.what')} />
      </DialogTitle>
      <DialogContent>
        <Tabs value={tab} onChange={(_, v) => setTab(v)} sx={{ mb: 1.5 }}>
          <Tab value="fronts" label={t('admin:servers.fronts.tab_fronts')} />
          <Tab value="ports" label={t('admin:servers.fronts.tab_ports')} />
        </Tabs>
        {tab === 'ports' ? <PortUsageTable panelId={server.id} /> : (
          <Stack spacing={1.5}>
            {fronts.isPending && <Box sx={{ display: 'flex', justifyContent: 'center', py: 3 }} role="status" aria-label={t('admin:servers.fronts.loading')}><CircularProgress size={24} /></Box>}
            {fronts.isError && <Alert severity="error">{t('admin:servers.fronts.load_failed')}</Alert>}
            {view && !supported && (
              <Alert severity="warning">{t(`admin:servers.fronts.unsupported.${view.support.reason ?? 'no_agent'}`)}</Alert>
            )}
            {view && supported && view.withheld.map(w => (
              <Alert key={w.front_id} severity="warning">
                {t('admin:servers.fronts.withheld.text', { port: w.port, why: t(`admin:servers.fronts.withheld.reason.${w.reason}`, { defaultValue: w.reason }), detail: w.detail ?? '' })}
              </Alert>
            ))}
            {view && view.fronts.length === 0 && <Typography variant="body2" color="text.secondary">{t('admin:servers.fronts.empty')}</Typography>}
            {view?.fronts.map(f => (
              <Box key={f.id} sx={{ border: 1, borderColor: 'divider', borderRadius: 1, p: 1.5 }}>
                <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                  <Typography variant="subtitle1" sx={{ flex: 1 }}>{t('admin:servers.fronts.front_title', { port: f.port })}</Typography>
                  {view && (!supported || view.withheld.some(w => w.front_id === f.id)) && (
                    <Chip size="small" color="warning" label={t('admin:servers.fronts.inactive')} />
                  )}
                  <Tooltip title={supported ? t('common:actions.edit') : t('admin:servers.fronts.unsupported_short')}>
                    <span>
                      <IconButton size="small" disabled={!supported || !optionData} aria-label={t('admin:servers.fronts.edit_port', { port: f.port })}
                        onClick={() => setEditing({ front: f })}><EditIcon fontSize="small" /></IconButton>
                    </span>
                  </Tooltip>
                  <IconButton size="small" aria-label={t('admin:servers.fronts.delete_port', { port: f.port })} onClick={() => void remove(f)}>
                    <DeleteIcon fontSize="small" />
                  </IconButton>
                </Box>
                {f.routes.map((r, i) => (
                  <Box key={i} sx={{ fontSize: 13, pl: 1 }}>
                    {t('admin:servers.fronts.ports.route_arrow', {
                      names: r.server_names.join(', '),
                      to: r.target.kind === 'listener'
                        ? (isTunnelListener(r.target.node_id) ? nodeName(r.target.node_id) : t('admin:servers.fronts.target.listener', { name: nodeName(r.target.node_id) }))
                        : behindText(t, { server_names: r.server_names, target: r.target, label: t('admin:servers.fronts.target.forward_short', { node: nodeName(r.target.node_id), line: r.target.line_id ?? '' }) }),
                    })}
                  </Box>
                ))}
                <Box sx={{ fontSize: 13, pl: 1 }}>
                  {t('admin:servers.fronts.ports.default_arrow', { to: `${f.default.host}:${f.default.port}` })}
                </Box>
                {f.listeners.length > 0 && (
                  <Box sx={{ pl: 1, pt: 0.5, display: 'flex', gap: 0.5, flexWrap: 'wrap' }}>
                    {f.listeners.map(l => (
                      <Chip key={l.node_id} size="small" variant="outlined"
                        label={t('admin:servers.fronts.listener_chip', { name: nodeName(l.node_id), port: l.loopback_port })} />
                    ))}
                  </Box>
                )}
              </Box>
            ))}
          </Stack>
        )}
      </DialogContent>
      <DialogActions>
        {tab === 'fronts' && (
          <Tooltip title={!supported && view ? t(`admin:servers.fronts.unsupported.${view.support.reason ?? 'no_agent'}`) : full ? t('admin:servers.fronts.full', { max: view?.max ?? 0 }) : ''}>
            <span>
              <Button variant="contained" disabled={!view || !supported || full || !optionData}
                onClick={() => setEditing({ front: null })}>{t('admin:servers.fronts.add')}</Button>
            </span>
          </Tooltip>
        )}
        <Button onClick={onClose}>{t('common:actions.close')}</Button>
      </DialogActions>
      {editing && view && optionData && (
        <FrontEditor key={editing.front?.id ?? 'new'} panelId={server.id} front={editing.front} options={optionData}
          defaults={view.defaults} onClose={() => setEditing(null)} />
      )}
    </Dialog>
  )
}
