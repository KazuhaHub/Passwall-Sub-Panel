import { Alert, Box, Button, FormControlLabel, IconButton, MenuItem, Switch, TextField, Tooltip, Typography, useTheme } from '@mui/material'
import AddIcon from '@mui/icons-material/Add'
import DeleteIcon from '@mui/icons-material/DeleteOutlined'
import ArrowUpwardIcon from '@mui/icons-material/ArrowUpward'
import ArrowDownwardIcon from '@mui/icons-material/ArrowDownward'
import RefreshIcon from '@mui/icons-material/Refresh'
import { useTranslation } from 'react-i18next'
import type { VlessRouteDraft } from '@/query/vlessRoute'
import type { VlessRouteLine } from '@/api/vlessRoute'

export default function VlessRouteLinesEditor({ draft, busy, hiddenDirect }: { draft: VlessRouteDraft; busy: boolean; hiddenDirect: boolean }) {
  const { t } = useTranslation(['admin', 'common'])
  const md = useTheme().palette.md
  const { baseline, lines, setLines, outbounds } = draft
  const disabled = busy || draft.loading || !baseline?.supported
  const move = (index: number, target: number) => setLines(previous => {
    const next = [...previous]; const [line] = next.splice(index, 1); next.splice(target, 0, line); return next
  })
  return <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
    <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', flexWrap: 'wrap', gap: 1 }}>
      <Typography sx={{ fontSize: 14, fontWeight: 600 }}>{t('admin:nodes.vless_route.title')}</Typography>
      <Box sx={{ display: 'flex', alignItems: 'center', ml: 'auto' }}>
        <Tooltip title={t('admin:nodes.vless_route.refresh')}>
          <span><IconButton size="small" aria-label={t('admin:nodes.vless_route.refresh')} disabled={busy || draft.refreshing || draft.loading} onClick={() => void draft.refreshOutbounds()}><RefreshIcon fontSize="small" /></IconButton></span>
        </Tooltip>
        <Button size="small" disabled={disabled || lines.length >= 64} startIcon={<AddIcon />} onClick={() => setLines(previous => [...previous, { name: '', outbound_tag: '', enabled: true }])}>{t('admin:nodes.vless_route.add')}</Button>
      </Box>
    </Box>
    <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>{t('admin:nodes.vless_route.hint')}</Typography>
    {draft.loading && <Alert severity="info">{t('common:status.loading')}</Alert>}
    {draft.readError && <Alert severity="error" action={<Button onClick={() => void draft.reload()}>{t('admin:nodes.vless_route.refresh')}</Button>}>{t('admin:nodes.vless_route.read_error')}</Alert>}
    {baseline && !baseline.supported && <Alert severity="info">{t('admin:nodes.vless_route.unsupported')}</Alert>}
    {draft.catalogError && <Alert severity="warning">{t('admin:nodes.vless_route.catalog_error')}</Alert>}
    {hiddenDirect && <Alert severity="warning">{t('admin:nodes.vless_route.hidden_direct')}</Alert>}
    {baseline?.state.status && baseline.state.status !== 'applied' && <Alert severity="warning">
      {t(`admin:nodes.vless_route.state_${baseline.state.status}`, { defaultValue: baseline.state.status })}
      {baseline.state.error && ` (${baseline.state.error})`}
    </Alert>}
    {baseline?.warnings.map((warning, index) => <Alert severity="warning" key={index}>{warning}</Alert>)}
    {!lines.length && !draft.loading && <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, fontStyle: 'italic' }}>{t('admin:nodes.vless_route.empty')}</Typography>}
    {lines.map((line, index) => {
      const update = (patch: Partial<VlessRouteLine>) => setLines(previous => previous.map((value, i) => i === index ? { ...value, ...patch } : value))
      const missing = !!line.outbound_tag && !outbounds.some(outbound => outbound.tag === line.outbound_tag)
      return <Box key={line.id || `new-${index}`} sx={{ border: `1px solid ${md.outlineVariant}`, borderRadius: 2, p: 1.5, display: 'flex', flexDirection: 'column', gap: 1 }}>
        <Box sx={{ display: 'flex', gap: { xs: 0.25, sm: 1 }, alignItems: 'center' }}>
          <TextField size="small" required label={t('admin:nodes.vless_route.name')} value={line.name} disabled={disabled} onChange={event => update({ name: event.target.value })} sx={{ flex: 1, minWidth: 0 }} />
          <FormControlLabel sx={{ m: 0, flexShrink: 0, '& .MuiFormControlLabel-label': { ml: 0.75, display: { xs: 'none', sm: 'block' } } }} label={t('admin:nodes.relay.enabled')} control={<Switch size="small" slotProps={{ input: { 'aria-label': t('admin:nodes.relay.enabled') } }} checked={line.enabled} disabled={disabled} onChange={(_, enabled) => update({ enabled })} />} />
          <Tooltip title={t('admin:rules.members.move_up')}>
            <span><IconButton size="small" aria-label={t('admin:rules.members.move_up')} disabled={disabled || index === 0} onClick={() => move(index, index - 1)}><ArrowUpwardIcon fontSize="small" /></IconButton></span>
          </Tooltip>
          <Tooltip title={t('admin:rules.members.move_down')}>
            <span><IconButton size="small" aria-label={t('admin:rules.members.move_down')} disabled={disabled || index === lines.length - 1} onClick={() => move(index, index + 1)}><ArrowDownwardIcon fontSize="small" /></IconButton></span>
          </Tooltip>
          <Tooltip title={t('admin:nodes.relay.remove')}>
            <IconButton size="small" aria-label={t('admin:nodes.relay.remove')} disabled={disabled} onClick={() => setLines(previous => previous.filter((_, i) => i !== index))}><DeleteIcon fontSize="small" /></IconButton>
          </Tooltip>
        </Box>
        <TextField select fullWidth size="small" required label={t('admin:nodes.vless_route.outbound')} value={line.outbound_tag} disabled={disabled} onChange={event => update({ outbound_tag: event.target.value })} error={missing} helperText={missing ? t('admin:nodes.vless_route.missing_outbound') : undefined}>
          {missing && <MenuItem value={line.outbound_tag}>{`${line.outbound_tag} [${t('admin:nodes.vless_route.unknown_protocol')}]`}</MenuItem>}
          {outbounds.map(outbound => <MenuItem key={outbound.tag} value={outbound.tag}>{`${outbound.tag} [${outbound.protocol || t('admin:nodes.vless_route.unknown_protocol')}]`}</MenuItem>)}
        </TextField>
      </Box>
    })}
  </Box>
}
