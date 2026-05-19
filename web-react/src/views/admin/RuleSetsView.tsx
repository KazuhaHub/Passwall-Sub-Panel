import { useEffect, useState, type FormEvent } from 'react'
import {
  Box,
  Button,
  Card,
  Checkbox,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  FormControlLabel,
  IconButton,
  Switch,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  TextField,
  Tooltip,
  Typography,
  useTheme,
} from '@mui/material'
import AddIcon from '@mui/icons-material/Add'
import DeleteIcon from '@mui/icons-material/DeleteOutline'
import RestartAltIcon from '@mui/icons-material/RestartAlt'
import EditIcon from '@mui/icons-material/EditOutlined'
import LockIcon from '@mui/icons-material/Lock'
import LockOpenIcon from '@mui/icons-material/LockOpen'
import { useTranslation } from 'react-i18next'

import { deleteRuleSet, listRuleSets, resetRuleSet, saveRuleSet, SEEDED_RULESET_SLUGS, type RuleSet } from '@/api/rules'
import { listTemplates, type Template } from '@/api/templates'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'

const EMPTY: RuleSet = {
  slug: '', name: '', sort: 100, enabled: true, proxy_group_order: [], content: '',
}

export default function RuleSetsView() {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation(['admin', 'common'])

  const [items, setItems] = useState<RuleSet[]>([])
  const [templates, setTemplates] = useState<Template[]>([])
  const [loading, setLoading] = useState(false)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [batchBusy, setBatchBusy] = useState<'enable' | 'disable' | 'delete' | ''>('')

  const [dialogOpen, setDialogOpen] = useState(false)
  const [editing, setEditing] = useState(false)
  const [form, setForm] = useState<RuleSet>(EMPTY)
  const [proxyGroupText, setProxyGroupText] = useState('')
  const [busy, setBusy] = useState(false)

  const allChecked = items.length > 0 && items.every(i => selected.has(i.slug))
  const someChecked = selected.size > 0 && !allChecked

  useEffect(() => { void load() }, [])

  async function load() {
    setLoading(true)
    try {
      const [rules, tpls] = await Promise.all([listRuleSets(), listTemplates()])
      setItems(rules); setTemplates(tpls); setSelected(new Set())
    } finally { setLoading(false) }
  }

  function toggleAll(checked: boolean) {
    setSelected(checked ? new Set(items.map(i => i.slug)) : new Set())
  }
  function toggleOne(slug: string, checked: boolean) {
    setSelected(prev => {
      const next = new Set(prev)
      if (checked) next.add(slug); else next.delete(slug)
      return next
    })
  }

  function openCreate() {
    setEditing(false); setForm(EMPTY); setProxyGroupText(''); setDialogOpen(true)
  }
  function openEdit(rs: RuleSet) {
    setEditing(true); setForm({ ...rs })
    setProxyGroupText((rs.proxy_group_order || []).join('\n'))
    setDialogOpen(true)
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (!form.slug || !form.name) {
      pushSnack(t('admin:rules.validate.slug_name_required'), 'warning'); return
    }
    setBusy(true)
    try {
      const proxyOrder = proxyGroupText.split('\n').map(s => s.trim()).filter(Boolean)
      await saveRuleSet({ ...form, proxy_group_order: proxyOrder })
      pushSnack(t('admin:rules.toast.saved'), 'success')
      setDialogOpen(false)
      await load()
    } finally { setBusy(false) }
  }

  function usedByTemplates(rs: RuleSet) {
    return templates.filter(tpl => (tpl.rule_sets || []).includes(rs.slug))
  }

  async function confirmDelete(rs: RuleSet) {
    const used = usedByTemplates(rs)
    const usage = used.length > 0
      ? t('admin:rules.confirm.delete_used_suffix', { names: used.map(x => x.name || x.slug).join('、') })
      : ''
    const ok = await confirm({
      title: t('admin:rules.confirm.delete_title'),
      message: t('admin:rules.confirm.delete_message', { slug: rs.slug, usage }),
      destructive: true,
    })
    if (!ok) return
    await deleteRuleSet(rs.slug)
    pushSnack(t('admin:rules.toast.deleted'), 'success')
    await load()
  }

  async function confirmReset(rs: RuleSet) {
    const ok = await confirm({
      title: t('admin:rules.confirm.reset_title'),
      message: t('admin:rules.confirm.reset_message', { slug: rs.slug }),
      destructive: true,
    })
    if (!ok) return
    await resetRuleSet(rs.slug)
    pushSnack(t('admin:rules.toast.reset'), 'success')
    await load()
  }

  async function batchSetEnabled(enabled: boolean) {
    const rows = items.filter(i => selected.has(i.slug))
    if (!rows.length) return
    setBatchBusy(enabled ? 'enable' : 'disable')
    try {
      const results = await Promise.allSettled(rows.map(r => saveRuleSet({ ...r, enabled })))
      const failed = results.filter(r => r.status === 'rejected').length
      if (failed > 0) pushSnack(t('admin:rules.toast.batch_partial', { ok: rows.length - failed, fail: failed }), 'warning')
      else pushSnack(t(enabled ? 'admin:rules.toast.batch_enabled' : 'admin:rules.toast.batch_disabled', { count: rows.length }), 'success')
      await load()
    } finally { setBatchBusy('') }
  }

  async function batchDelete() {
    const rows = items.filter(i => selected.has(i.slug))
    if (!rows.length) return
    const ok = await confirm({
      title: t('admin:rules.confirm.batch_delete_title'),
      message: t('admin:rules.confirm.batch_delete_message', { count: rows.length }),
      destructive: true,
    })
    if (!ok) return
    setBatchBusy('delete')
    try {
      const results = await Promise.allSettled(rows.map(r => deleteRuleSet(r.slug)))
      const okSlugs = rows.filter((_, i) => results[i].status === 'fulfilled').map(r => r.slug)
      const failed = rows.length - okSlugs.length
      setItems(prev => prev.filter(x => !okSlugs.includes(x.slug)))
      setSelected(new Set())
      if (failed > 0) pushSnack(t('admin:rules.toast.batch_partial', { ok: okSlugs.length, fail: failed }), 'warning')
      else pushSnack(t('admin:rules.toast.batch_deleted', { count: okSlugs.length }), 'success')
    } finally { setBatchBusy('') }
  }

  function countLines(s: string): number {
    return s ? s.split('\n').filter(l => l.trim()).length : 0
  }

  function badge(label: string, bg: string, fg: string) {
    return <Box sx={{
      display: 'inline-block', px: 1.25, py: 0.25,
      borderRadius: 1, fontSize: 12, fontWeight: 500,
      bgcolor: bg, color: fg, whiteSpace: 'nowrap',
    }}>{label}</Box>
  }

  return (
    <Box sx={{ p: 3 }}>
      <Box sx={{ display: 'flex', alignItems: 'flex-end', justifyContent: 'space-between', flexWrap: 'wrap', gap: 2, mb: 1 }}>
        <Box>
          <Typography variant="h4">{t('admin:rules.title')}</Typography>
          <Typography variant="body2" sx={{ mt: 0.5 }}>{t('admin:rules.subtitle')}</Typography>
        </Box>
        <Button variant="contained" startIcon={<AddIcon />} onClick={openCreate}>
          {t('admin:rules.create')}
        </Button>
      </Box>

      {selected.size > 0 && (
        <Box sx={{
          display: 'flex', alignItems: 'center', gap: 1, mt: 2, mb: 1, px: 2, py: 1,
          borderRadius: 9999, bgcolor: md.secondaryContainer, color: md.onSecondaryContainer,
          width: 'fit-content',
        }}>
          <Typography sx={{ fontSize: 13, fontWeight: 500, mr: 1 }}>
            {t('admin:rules.selection_count', { count: selected.size })}
          </Typography>
          <Button size="small" variant="text" sx={{ color: 'inherit' }}
            startIcon={batchBusy === 'enable' ? <CircularProgress size={14} /> : <LockOpenIcon />}
            disabled={batchBusy !== ''} onClick={() => batchSetEnabled(true)}>
            {t('admin:rules.batch_enable')}
          </Button>
          <Button size="small" variant="text" sx={{ color: 'inherit' }}
            startIcon={batchBusy === 'disable' ? <CircularProgress size={14} /> : <LockIcon />}
            disabled={batchBusy !== ''} onClick={() => batchSetEnabled(false)}>
            {t('admin:rules.batch_disable')}
          </Button>
          <Button size="small" variant="text" color="error"
            startIcon={batchBusy === 'delete' ? <CircularProgress size={14} /> : <DeleteIcon />}
            disabled={batchBusy !== ''} onClick={batchDelete}>
            {t('admin:rules.batch_delete')}
          </Button>
        </Box>
      )}

      <Card sx={{ mt: 2, bgcolor: md.surfaceContainerLow, boxShadow: '0 1px 2px rgba(0,0,0,.3),0 1px 3px 1px rgba(0,0,0,.15)', overflow: 'hidden' }}>
        <TableContainer>
          <Table>
            <TableHead>
              <TableRow sx={{ '& th': { color: md.onSurfaceVariant, fontWeight: 500, fontSize: 12, textTransform: 'uppercase', letterSpacing: '.5px', borderBottom: `1px solid ${md.outlineVariant}`, whiteSpace: 'nowrap' } }}>
                <TableCell padding="checkbox">
                  <Checkbox indeterminate={someChecked} checked={allChecked}
                    onChange={(_, c) => toggleAll(c)} disabled={items.length === 0} />
                </TableCell>
                <TableCell>{t('admin:rules.table.slug')}</TableCell>
                <TableCell>{t('admin:rules.table.name')}</TableCell>
                <TableCell align="right">{t('admin:rules.table.sort')}</TableCell>
                <TableCell align="right">{t('admin:rules.table.lines')}</TableCell>
                <TableCell>{t('admin:rules.table.used_by')}</TableCell>
                <TableCell>{t('admin:rules.table.status')}</TableCell>
                <TableCell align="right">{t('admin:rules.table.actions')}</TableCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {loading && items.length === 0 && (
                <TableRow><TableCell colSpan={8} sx={{ textAlign: 'center', py: 6 }}>
                  <CircularProgress size={24} />
                </TableCell></TableRow>
              )}
              {!loading && items.length === 0 && (
                <TableRow><TableCell colSpan={8} sx={{ textAlign: 'center', py: 6, color: md.onSurfaceVariant }}>—</TableCell></TableRow>
              )}
              {items.map(rs => {
                const used = usedByTemplates(rs)
                return (
                  <TableRow key={rs.slug} hover sx={{
                    '& td': { borderBottom: `1px solid ${md.outlineVariant}`, whiteSpace: 'nowrap' },
                    opacity: rs.enabled ? 1 : 0.65,
                  }}>
                    <TableCell padding="checkbox">
                      <Checkbox checked={selected.has(rs.slug)} onChange={(_, c) => toggleOne(rs.slug, c)} />
                    </TableCell>
                    <TableCell sx={{ fontSize: 13 }}>{rs.slug}</TableCell>
                    <TableCell sx={{ fontWeight: 500 }}>{rs.name}</TableCell>
                    <TableCell align="right" sx={{ fontVariantNumeric: 'tabular-nums' }}>{rs.sort}</TableCell>
                    <TableCell align="right" sx={{ fontVariantNumeric: 'tabular-nums', fontSize: 13 }}>{countLines(rs.content)}</TableCell>
                    <TableCell sx={{ fontSize: 13, color: used.length === 0 ? md.onSurfaceVariant : md.onSurface, maxWidth: 280, overflow: 'hidden', textOverflow: 'ellipsis' }}>
                      {used.length === 0 ? t('admin:rules.status.no_usage') : used.map(x => x.name || x.slug).join('、')}
                    </TableCell>
                    <TableCell>
                      {rs.enabled
                        ? badge(t('admin:rules.status.enabled'), md.tertiaryContainer, md.onTertiaryContainer)
                        : badge(t('admin:rules.status.disabled'), md.surfaceContainerHighest, md.onSurfaceVariant)}
                    </TableCell>
                    <TableCell align="right">
                      <Tooltip title={t('admin:rules.field.enabled')}>
                        <IconButton size="small" onClick={() => openEdit(rs)}><EditIcon fontSize="small" /></IconButton>
                      </Tooltip>
                      {SEEDED_RULESET_SLUGS.includes(rs.slug) && (
                        <Tooltip title={t('admin:rules.reset_to_default')}>
                          <IconButton size="small" onClick={() => confirmReset(rs)}
                            sx={{ color: md.onSurfaceVariant }}>
                            <RestartAltIcon fontSize="small" />
                          </IconButton>
                        </Tooltip>
                      )}
                      <Tooltip title={t('admin:rules.batch_delete')}>
                        <IconButton size="small" onClick={() => confirmDelete(rs)} sx={{ color: md.error }}>
                          <DeleteIcon fontSize="small" />
                        </IconButton>
                      </Tooltip>
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        </TableContainer>
      </Card>

      {/* Create/Edit dialog */}
      <Dialog open={dialogOpen} onClose={() => !busy && setDialogOpen(false)}
        PaperProps={{ sx: { borderRadius: 3, bgcolor: md.surfaceContainerHigh, width: 720, maxWidth: '95vw' } }}>
        <DialogTitle>
          {editing ? t('admin:rules.edit_title') : t('admin:rules.create')}
        </DialogTitle>
        <DialogContent>
          <Box component="form" id="rules-form" onSubmit={submit} sx={{ display: 'flex', flexDirection: 'column', gap: 2.5, pt: 1 }}>
            <TextField required fullWidth label={t('admin:rules.field.slug')}
              value={form.slug} disabled={editing}
              onChange={e => setForm({ ...form, slug: e.target.value })}
              placeholder={t('admin:rules.placeholder.slug')}
              sx={{ '& input': {  } }} />
            <TextField required fullWidth label={t('admin:rules.field.name')}
              value={form.name}
              onChange={e => setForm({ ...form, name: e.target.value })}
              placeholder={t('admin:rules.placeholder.name')} />
            <Box sx={{ display: 'flex', gap: 2 }}>
              <TextField type="number" label={t('admin:rules.field.sort')}
                value={form.sort}
                onChange={e => setForm({ ...form, sort: Number(e.target.value) })}
                sx={{ width: 160 }} />
              <FormControlLabel
                label={t('admin:rules.field.enabled')}
                control={<Switch checked={form.enabled} onChange={(_, c) => setForm({ ...form, enabled: c })} />}
                sx={{ ml: 1, '& .MuiFormControlLabel-label': { ml: 1.5 } }}
              />
            </Box>
            <TextField fullWidth multiline minRows={4} maxRows={8}
              label={t('admin:rules.field.proxy_group_order')}
              placeholder={t('admin:rules.placeholder.proxy_group_order')}
              helperText={t('admin:rules.hint.proxy_group_order')}
              value={proxyGroupText}
              onChange={e => setProxyGroupText(e.target.value)}
              sx={{ '& textarea': { fontSize: 13 } }} />
            <TextField fullWidth multiline minRows={10} maxRows={20}
              label={t('admin:rules.field.content')}
              placeholder={t('admin:rules.placeholder.content')}
              value={form.content}
              onChange={e => setForm({ ...form, content: e.target.value })}
              sx={{ '& textarea': { fontSize: 13 } }} />
          </Box>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setDialogOpen(false)} disabled={busy} variant="text">{t('common:actions.cancel')}</Button>
          <Button type="submit" form="rules-form" variant="contained" disabled={busy}
            startIcon={busy ? <CircularProgress size={16} color="inherit" /> : null}>
            {t('common:actions.ok')}
          </Button>
        </DialogActions>
      </Dialog>
    </Box>
  )
}
