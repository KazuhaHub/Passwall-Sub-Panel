import { useMemo, useState } from 'react'
import {
  Alert, Box, Button, Checkbox, CircularProgress, Dialog, DialogActions, DialogContent, DialogTitle, FormControlLabel,
  List, ListItem, Stack, TextField, Typography,
} from '@mui/material'
import { useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { updateServerLineFactors, type LineFactorChange, type Server } from '@/api/servers'
import { pushSnack } from '@/components/SnackbarHost'
import { nodeKeys } from '@/query/keys'
import { useNodesList } from '@/query/nodes'
import { useQueryScope } from '@/query/useQueryScope'
import { DEFAULT_TRAFFIC_FACTOR_MILLI, factorTextToMilli, milliToFactorText } from '@/utils/trafficFactor'

const NODES_PARAMS = { page: 1, page_size: 500 } as const

interface LineRow { key: string; node_id: number; line_id: string; label: string; milli: number }

/** Bulk traffic multiplier editor for every line of the nodes on one server. */
/** Mount only while open: the node list is fetched on mount. */
export function LineMultipliersDialog({ server, onClose }: { server: Server; onClose: () => void }) {
  const { t } = useTranslation(['admin', 'common'])
  const scope = useQueryScope()
  const queryClient = useQueryClient()
  const nodes = useNodesList(scope, NODES_PARAMS)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [text, setText] = useState('1')
  const [busy, setBusy] = useState(false)
  const [applyError, setApplyError] = useState('')

  const rows = useMemo<LineRow[]>(() => {
    const out: LineRow[] = []
    for (const n of nodes.data?.items ?? []) {
      if (n.panel_id !== server.id || (n as { kind?: string }).kind === 'separator') continue
      out.push({ key: `${n.id}:direct`, node_id: n.id, line_id: 'direct', label: n.display_name, milli: n.traffic_factor_milli ?? DEFAULT_TRAFFIC_FACTOR_MILLI })
      for (const r of n.relays ?? []) {
        if (!r.id) continue
        out.push({ key: `${n.id}:${r.id}`, node_id: n.id, line_id: r.id, label: `${n.display_name} / ${r.name || (r.mode === 'cdn' && r.domain) || r.address}`, milli: r.traffic_factor_milli ?? DEFAULT_TRAFFIC_FACTOR_MILLI })
      }
    }
    return out
  }, [server, nodes.data])

  const milli = factorTextToMilli(text)
  const selectedRows = rows.filter(r => selected.has(r.key))
  const allSelected = rows.length > 0 && rows.every(r => selected.has(r.key))

  function toggle(key: string, on: boolean) {
    setSelected(prev => { const next = new Set(prev); if (on) next.add(key); else next.delete(key); return next })
  }

  async function apply() {
    if (milli === null) return
    const lines: LineFactorChange[] = selectedRows
      .map(r => ({ node_id: r.node_id, line_id: r.line_id, traffic_factor_milli: milli }))
    if (lines.length === 0) return
    setBusy(true)
    setApplyError('')
    try {
      const res = await updateServerLineFactors(server.id, lines)
      await queryClient.invalidateQueries({ queryKey: nodeKeys.all(scope) })
      pushSnack(t('admin:servers.line_factors.updated', { count: res.changed }), 'success')
      setSelected(new Set())
    } catch (err) {
      setApplyError(t('admin:servers.line_factors.apply_failed', { message: err instanceof Error ? err.message : String(err) }))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open onClose={onClose} fullWidth maxWidth="sm">
      <DialogTitle>{t('admin:servers.line_factors.title', { name: server.name })}</DialogTitle>
      <DialogContent>
        <Stack spacing={1.5} sx={{ pt: 1 }}>
          <Typography variant="body2" color="text.secondary">{t('admin:servers.line_factors.hint')}</Typography>
          {nodes.isPending ? (
            <Box sx={{ display: 'flex', justifyContent: 'center', py: 3 }} role="status" aria-label={t('admin:servers.line_factors.loading')}><CircularProgress size={24} /></Box>
          ) : nodes.isError ? (
            <Alert severity="error">{t('admin:servers.line_factors.load_failed')}</Alert>
          ) : rows.length === 0 ? (
            <Typography variant="body2">{t('admin:servers.line_factors.empty')}</Typography>
          ) : (
            <>
              <FormControlLabel
                label={t('admin:servers.line_factors.select_all')}
                control={<Checkbox checked={allSelected} indeterminate={!allSelected && selected.size > 0}
                  onChange={(_, c) => setSelected(c ? new Set(rows.map(r => r.key)) : new Set())} />}
              />
              <List dense disablePadding sx={{ maxHeight: 320, overflow: 'auto' }}>
                {rows.map(r => (
                  <ListItem key={r.key} disableGutters sx={{ py: 0 }}>
                    <FormControlLabel
                      sx={{ flex: 1, mr: 1 }}
                      control={<Checkbox size="small" checked={selected.has(r.key)} onChange={(_, c) => toggle(r.key, c)} />}
                      label={<Box sx={{ display: 'flex', alignItems: 'center', gap: 0.75 }}>
                        <span>{r.label}</span>
                        <Typography component="span" variant="caption" color="text.secondary">×{milliToFactorText(r.milli)}</Typography>
                      </Box>}
                    />
                  </ListItem>
                ))}
              </List>
            </>
          )}
          {applyError && <Alert severity="error">{applyError}</Alert>}
          <TextField
            type="number" label={t('admin:servers.line_factors.field')} value={text}
            onChange={e => setText(e.target.value)}
            error={milli === null}
            helperText={milli === null ? t('admin:servers.line_factors.invalid') : t('admin:servers.line_factors.field_hint')}
            slotProps={{ htmlInput: { min: 0, max: 100, step: 0.001, inputMode: 'decimal' } }}
          />
        </Stack>
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose}>{t('common:actions.cancel')}</Button>
        <Button variant="contained" onClick={apply} disabled={busy || milli === null || selectedRows.length === 0}>
          {t('admin:servers.line_factors.apply')}
        </Button>
      </DialogActions>
    </Dialog>
  )
}
