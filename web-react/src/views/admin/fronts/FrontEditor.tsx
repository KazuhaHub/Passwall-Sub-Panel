import { useMemo, useState } from 'react'
import {
  Alert, Box, Button, Checkbox, Dialog, DialogActions, DialogContent, DialogTitle, FormControlLabel, IconButton,
  MenuItem, Stack, TextField, Tooltip, Typography,
} from '@mui/material'
import DeleteIcon from '@mui/icons-material/Delete'
import { useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import {
  createFront, suggestCamouflage, suggestLoopbackPort, updateFront,
  type Front, type FrontOptions, type FrontsView,
} from '@/api/fronts'
import InfoTip from '@/components/InfoTip'
import { pushSnack } from '@/components/SnackbarHost'
import { frontKeys } from '@/query/keys'
import { useQueryScope } from '@/query/useQueryScope'
import {
  draftIssues, draftOf, inputOf, leaving, linesFor, listenersFor, newRouteKey, newlyMoved, targetKey, type Draft,
} from './frontDraft'
import { frontErrorText, issueText } from './frontErrors'

interface Props {
  panelId: number
  front: Front | null
  options: FrontOptions
  defaults: FrontsView['defaults']
  onClose: () => void
}

/** One shared port. Every value is the admin's: the buttons only fill fields in. */
export default function FrontEditor({ panelId, front, options, defaults, onClose }: Props) {
  const { t } = useTranslation(['admin', 'common'])
  const scope = useQueryScope()
  const queryClient = useQueryClient()
  const [draft, setDraft] = useState<Draft>(() => draftOf(front, defaults.port))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const port = Number(draft.port)
  const listeners = listenersFor(options, port, draft)
  const lines = linesFor(options, port)
  const nodeName = (id: number) => options.listeners.find(l => l.node_id === id)?.name ?? `#${id}`

  // What a route can point at: the listeners the draft moves behind the front
  // and the relay lines entering on this port.
  const targets = useMemo(() => {
    const out: { key: string; label: string; names: string[] }[] = []
    for (const l of draft.listeners) {
      const o = options.listeners.find(x => x.node_id === l.node_id)
      out.push({ key: targetKey.listener(l.node_id), label: t('admin:servers.fronts.target.listener', { name: o?.name ?? `#${l.node_id}` }), names: o?.server_names ?? [] })
    }
    for (const line of lines) {
      out.push({
        key: targetKey.forward(line.node_id, line.line_id),
        label: t('admin:servers.fronts.target.forward', { node: line.node_name, line: line.line_name }), names: line.server_names,
      })
    }
    return out
  }, [draft.listeners, lines, options.listeners, t])
  const labelOf = (key: string) => targets.find(x => x.key === key)?.label ?? ''

  const issues = draftIssues(draft, options, { low: defaults.probe_low, high: defaults.probe_high }, labelOf)
  const moved = newlyMoved(draft, front)
  const away = leaving(draft, front)
  const needsConfirm = moved.length + away.listeners.length + away.lines.length > 0
  const canSave = !busy && issues.length === 0 && (!needsConfirm || draft.confirm)

  const patch = (p: Partial<Draft>) => setDraft(d => ({ ...d, ...p }))

  async function toggleListener(nodeId: number, on: boolean) {
    if (!on) {
      setDraft(d => ({ ...d, listeners: d.listeners.filter(l => l.node_id !== nodeId), routes: d.routes.filter(r => r.target !== targetKey.listener(nodeId)) }))
      return
    }
    const option = options.listeners.find(l => l.node_id === nodeId)
    let loopback = ''
    try { loopback = String(await suggestLoopbackPort(panelId)) } catch { /* the field stays empty to be typed */ }
    setDraft(d => ({
      ...d,
      listeners: [...d.listeners, { node_id: nodeId, loopback }],
      routes: [...d.routes, { key: newRouteKey(), names: (option?.server_names ?? []).join(', '), target: targetKey.listener(nodeId) }],
    }))
  }

  async function randomLoopback(nodeId: number) {
    try {
      const p = await suggestLoopbackPort(panelId)
      setDraft(d => ({ ...d, listeners: d.listeners.map(l => l.node_id === nodeId ? { ...l, loopback: String(p) } : l) }))
    } catch (err) { setError(frontErrorText(t, err)) }
  }

  async function randomSite() {
    try {
      const s = await suggestCamouflage(panelId)
      patch({ host: s.host, defaultPort: String(s.port) })
    } catch (err) { setError(frontErrorText(t, err)) }
  }

  function setRouteTarget(key: string, target: string) {
    setDraft(d => ({
      ...d,
      routes: d.routes.map(r => {
        if (r.key !== key) return r
        const names = r.names.trim() === '' ? (targets.find(x => x.key === target)?.names ?? []).join(', ') : r.names
        return { ...r, target, names }
      }),
    }))
  }

  async function save() {
    setBusy(true)
    setError('')
    try {
      const input = inputOf(draft)
      const saved = front ? await updateFront(panelId, front.id, input) : await createFront(panelId, input)
      await queryClient.invalidateQueries({ queryKey: frontKeys.all(scope) })
      pushSnack(t('admin:servers.fronts.saved'), 'success')
      if (saved.warnings?.includes('default_unresolved')) pushSnack(t('admin:servers.fronts.warning.default_unresolved'), 'warning')
      onClose()
    } catch (err) {
      setError(frontErrorText(t, err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open onClose={onClose} fullWidth maxWidth="md">
      <DialogTitle>{front ? t('admin:servers.fronts.edit_title', { port: front.port }) : t('admin:servers.fronts.new_title')}</DialogTitle>
      <DialogContent>
        <Stack spacing={2.5} sx={{ pt: 1 }}>
          <Box sx={{ display: 'flex', alignItems: 'flex-start', gap: 0.5 }}>
            <TextField
              label={t('admin:servers.fronts.port')} value={draft.port} onChange={e => patch({ port: e.target.value })}
              helperText={t('admin:servers.fronts.port_hint')} sx={{ width: 220 }}
              slotProps={{ htmlInput: { inputMode: 'numeric' } }}
            />
            <InfoTip text={t('admin:servers.fronts.help.what')} />
          </Box>

          <Box>
            <Typography variant="subtitle2" sx={{ display: 'flex', alignItems: 'center' }}>
              {t('admin:servers.fronts.listeners')}
              <InfoTip text={t('admin:servers.fronts.help.listeners')} />
            </Typography>
            {listeners.length === 0 && <Typography variant="body2" color="text.secondary">{t('admin:servers.fronts.no_listeners')}</Typography>}
            {listeners.map(l => {
              const chosen = draft.listeners.find(x => x.node_id === l.node_id)
              return (
                <Box key={l.node_id} sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap', py: 0.5 }}>
                  <FormControlLabel
                    sx={{ minWidth: 260 }}
                    control={<Checkbox size="small" checked={!!chosen} disabled={!l.frontable}
                      onChange={(_, c) => void toggleListener(l.node_id, c)} />}
                    label={`${l.name} (${l.protocol}${l.security ? `+${l.security}` : ''}, :${l.port})`}
                  />
                  {!l.frontable && (
                    <Typography variant="caption" color="warning.main">{t(`admin:servers.fronts.refusal.${l.reason ?? 'no_sni'}`)}</Typography>
                  )}
                  {chosen && (
                    <>
                      <TextField
                        size="small" label={t('admin:servers.fronts.loopback')} value={chosen.loopback} sx={{ width: 170 }}
                        onChange={e => setDraft(d => ({ ...d, listeners: d.listeners.map(x => x.node_id === l.node_id ? { ...x, loopback: e.target.value } : x) }))}
                        slotProps={{ htmlInput: { inputMode: 'numeric', 'aria-label': t('admin:servers.fronts.loopback_of', { name: l.name }) } }}
                      />
                      <Button size="small" onClick={() => void randomLoopback(l.node_id)}>{t('admin:servers.fronts.random')}</Button>
                    </>
                  )}
                </Box>
              )
            })}
          </Box>

          <Box>
            <Typography variant="subtitle2" sx={{ display: 'flex', alignItems: 'center' }}>
              {t('admin:servers.fronts.routes')}
              <InfoTip text={t('admin:servers.fronts.help.routes')} />
            </Typography>
            {draft.routes.map((r, index) => (
              <Box key={r.key} sx={{ display: 'flex', gap: 1, alignItems: 'flex-start', py: 0.75 }}>
                <TextField
                  size="small" label={t('admin:servers.fronts.names')} value={r.names} sx={{ flex: 2 }}
                  helperText={t('admin:servers.fronts.names_hint')}
                  onChange={e => setDraft(d => ({ ...d, routes: d.routes.map(x => x.key === r.key ? { ...x, names: e.target.value } : x) }))}
                  slotProps={{ htmlInput: { 'aria-label': t('admin:servers.fronts.names_of', { route: index + 1 }) } }}
                />
                <TextField
                  select size="small" label={t('admin:servers.fronts.goes_to')} value={r.target} sx={{ flex: 2 }}
                  onChange={e => setRouteTarget(r.key, e.target.value)}
                  slotProps={{ htmlInput: { 'aria-label': t('admin:servers.fronts.target_of', { route: index + 1 }) } }}
                >
                  {targets.map(x => <MenuItem key={x.key} value={x.key}>{x.label}</MenuItem>)}
                </TextField>
                <Tooltip title={t('admin:servers.fronts.remove_route')}>
                  <IconButton size="small" aria-label={t('admin:servers.fronts.remove_route')}
                    onClick={() => setDraft(d => ({ ...d, routes: d.routes.filter(x => x.key !== r.key) }))}>
                    <DeleteIcon fontSize="small" />
                  </IconButton>
                </Tooltip>
              </Box>
            ))}
            <Button size="small" disabled={targets.length === 0}
              onClick={() => setDraft(d => ({ ...d, routes: [...d.routes, { key: newRouteKey(), names: '', target: '' }] }))}>
              {t('admin:servers.fronts.add_route')}
            </Button>
            {targets.length === 0 && <Typography variant="caption" color="text.secondary" sx={{ ml: 1 }}>{t('admin:servers.fronts.no_targets')}</Typography>}
          </Box>

          <Box>
            <Typography variant="subtitle2" sx={{ display: 'flex', alignItems: 'center' }}>
              {t('admin:servers.fronts.default')}
              <InfoTip text={t('admin:servers.fronts.help.default')} />
            </Typography>
            <Box sx={{ display: 'flex', gap: 1, alignItems: 'flex-start', pt: 0.75, flexWrap: 'wrap' }}>
              <TextField size="small" label={t('admin:servers.fronts.default_host')} value={draft.host} sx={{ minWidth: 260 }}
                onChange={e => patch({ host: e.target.value })} helperText={t('admin:servers.fronts.default_host_hint')} />
              <TextField size="small" label={t('admin:servers.fronts.default_port')} value={draft.defaultPort} sx={{ width: 110 }}
                onChange={e => patch({ defaultPort: e.target.value })} slotProps={{ htmlInput: { inputMode: 'numeric' } }} />
              <Button size="small" onClick={() => void randomSite()}>{t('admin:servers.fronts.random')}</Button>
            </Box>
          </Box>

          {needsConfirm && (
            <Alert severity="warning">
              <Typography variant="body2" sx={{ mb: 0.5 }}>
                {[
                  moved.length > 0 && t('admin:servers.fronts.confirm_text', { nodes: moved.map(nodeName).join(', '), port: draft.port }),
                  away.listeners.length > 0 && t('admin:servers.fronts.confirm_leaving_listeners', { nodes: away.listeners.map(nodeName).join(', ') }),
                  away.lines.length > 0 && t('admin:servers.fronts.confirm_leaving_lines', { count: away.lines.length }),
                ].filter(Boolean).join(' ')}
              </Typography>
              <FormControlLabel
                control={<Checkbox size="small" checked={draft.confirm} onChange={(_, c) => patch({ confirm: c })} />}
                label={t('admin:servers.fronts.confirm_label')}
              />
            </Alert>
          )}
          {issues.length > 0 && (
            <Alert severity="info" role="status">
              <ul style={{ margin: 0, paddingLeft: 18 }}>
                {issues.map((issue, i) => <li key={i}>{issueText(t, issue, nodeName)}</li>)}
              </ul>
            </Alert>
          )}
          {error && <Alert severity="error">{error}</Alert>}
        </Stack>
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose}>{t('common:actions.cancel')}</Button>
        <Button variant="contained" onClick={() => void save()} disabled={!canSave}>{t('common:actions.save')}</Button>
      </DialogActions>
    </Dialog>
  )
}
