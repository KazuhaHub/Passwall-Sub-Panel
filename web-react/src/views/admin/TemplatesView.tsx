import { useEffect, useMemo, useState, type FormEvent } from 'react'
import {
  Box,
  Button,
  Card,
  Checkbox,
  Chip,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  FormControl,
  FormControlLabel,
  IconButton,
  InputLabel,
  ListItemText,
  MenuItem,
  OutlinedInput,
  Select,
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
import ContentCopyIcon from '@mui/icons-material/ContentCopyOutlined'
import DeleteIcon from '@mui/icons-material/DeleteOutlined'
import EditIcon from '@mui/icons-material/EditOutlined'
import RestartAltIcon from '@mui/icons-material/RestartAlt'
import { useTranslation } from 'react-i18next'
import { useCan } from '@/utils/permissions'

import { deleteTemplate, listTemplates, resetTemplate, saveTemplate, SEEDED_TEMPLATE_SLUGS, type Template } from '@/api/templates'
import { listRuleSets, type RuleSet } from '@/api/rules'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'
import { PagedTableFooter } from '@/components/PagedTableFooter'
import PageHeader from '@/components/PageHeader'

const EMPTY: Template = {
  slug: '', name: '', client_type: 'mihomo', is_default: false, rule_sets: [], content: '',
}

const BUILTIN_TARGETS = new Set(['DIRECT', 'REJECT', 'REJECT-DROP', 'REJECT-DROP-BIT', 'PASS'])

function normalizeTarget(raw: string) { return raw.trim().replace(/^['"]|['"]$/g, ''); }

function extractProxyGroups(content: string): Set<string> {
  const groups = new Set<string>()
  let inProxyGroups = false
  for (const line of content.split('\n')) {
    if (/^proxy-groups\s*:/.test(line)) { inProxyGroups = true; continue }
    if (inProxyGroups && /^\S/.test(line) && !/^proxy-groups\s*:/.test(line)) break
    const match = line.match(/^\s*-\s*name:\s*(.+?)\s*$/)
    if (inProxyGroups && match) groups.add(normalizeTarget(match[1]))
  }
  return groups
}

function usesDynamicProxyGroups(content: string) {
  return /\{\{\s*proxy_groups\s*\}\}/.test(content) || /\{\{\s*outbounds\s*\}\}/.test(content);
}

function extractRuleTargets(sets: RuleSet[]): Set<string> {
  const targets = new Set<string>()
  for (const rs of sets) {
    if (!rs.enabled) continue
    for (const rawLine of rs.content.split('\n')) {
      const line = rawLine.trim().replace(/^-\s*/, '')
      if (!line || line.startsWith('#') || line.includes('{{')) continue
      const parts = line.split(',').map(p => normalizeTarget(p))
      if (parts.length < 2) continue
      const useful = parts.filter(p => p && p !== 'no-resolve')
      const target = useful[useful.length - 1]
      if (target && !BUILTIN_TARGETS.has(target)) targets.add(target)
    }
  }
  return targets
}

function missingTargets(groups: Set<string>, targets: Set<string>): string[] {
  if (groups.size === 0) return Array.from(targets)
  return Array.from(targets).filter(t => !groups.has(t))
}

export default function TemplatesView() {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation(['admin', 'common'])
  const canConfig = useCan('config.write')

  const [items, setItems] = useState<Template[]>([])
  const [ruleSets, setRuleSets] = useState<RuleSet[]>([])
  const [loading, setLoading] = useState(false)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [batchBusy, setBatchBusy] = useState(false)

  const [dialogOpen, setDialogOpen] = useState(false)
  const [editing, setEditing] = useState(false)
  const [form, setForm] = useState<Template>(EMPTY)
  const [busy, setBusy] = useState(false)

  // Client-side pagination — template lists are small but consistent
  // UX wants the same footer everywhere.
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState<number>(() => {
    try {
      const raw = localStorage.getItem('psp_page_size')
      const n = raw ? parseInt(raw, 10) : 25
      return Number.isFinite(n) && n > 0 ? n : 25
    } catch { return 25 }
  })
  function changePageSize(n: number) {
    setPageSize(n)
    try { localStorage.setItem('psp_page_size', String(n)) } catch { /* ignore */ }
    setPage(1)
  }
  const pagedItems = useMemo(
    () => items.slice((page - 1) * pageSize, page * pageSize),
    [items, page, pageSize],
  )

  const selectableSlugs = pagedItems.filter(i => !i.is_default).map(i => i.slug)
  const allChecked = selectableSlugs.length > 0 && selectableSlugs.every(s => selected.has(s))
  const someChecked = selected.size > 0 && !allChecked

  const ruleSetMap = useMemo(() => new Map(ruleSets.map(r => [r.slug, r])), [ruleSets])

  useEffect(() => { void load() }, [])

  async function load() {
    setLoading(true)
    try {
      const [tpls, rules] = await Promise.all([listTemplates(), listRuleSets()])
      setItems(tpls.items); setRuleSets(rules.items); setSelected(new Set())
    } finally { setLoading(false) }
  }

  function toggleAll(checked: boolean) {
    // Per-page semantics matching ServersView / RuleSetsView /
    // GroupsView: only flip the currently visible page's selectable
    // rows. Pre-v3.6.1-beta.5 the box wiped or replaced the entire
    // cross-page selection, which surprised admins who had checked
    // rows on a different page.
    setSelected(prev => {
      const next = new Set(prev)
      selectableSlugs.forEach(s => { if (checked) next.add(s); else next.delete(s) })
      return next
    })
  }
  function toggleOne(slug: string, checked: boolean) {
    setSelected(prev => {
      const next = new Set(prev)
      if (checked) next.add(slug); else next.delete(slug)
      return next
    })
  }

  function openCreate() { setEditing(false); setForm(EMPTY); setDialogOpen(true) }
  function openEdit(tpl: Template) {
    setEditing(true)
    setForm({ ...tpl, rule_sets: tpl.rule_sets ? [...tpl.rule_sets] : [] })
    setDialogOpen(true)
  }
  // Duplicate clones the row into the create flow so the admin can
  // tweak the slug + name before saving. is_default is forced off so
  // the new copy doesn't fight the original for the per-client-type
  // default — and so the seeded original keeps its protection /
  // canonical-default semantics.
  function openDuplicate(tpl: Template) {
    setEditing(false)
    setForm({
      ...tpl,
      slug: tpl.slug + '-copy',
      name: tpl.name + ' (Copy)',
      is_default: false,
      rule_sets: tpl.rule_sets ? [...tpl.rule_sets] : [],
    })
    setDialogOpen(true)
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (!form.slug || !form.name) {
      pushSnack(t('admin:templates.validate.slug_name_required'), 'warning'); return
    }
    setBusy(true)
    try {
      await saveTemplate({ ...form })
      pushSnack(t('admin:templates.toast.saved'), 'success')
      setDialogOpen(false)
      await load()
    } finally { setBusy(false) }
  }

  async function confirmDelete(tpl: Template) {
    const ok = await confirm({
      title: t('admin:templates.confirm.delete_title'),
      message: t('admin:templates.confirm.delete_message', { slug: tpl.slug }),
      destructive: true,
    })
    if (!ok) return
    await deleteTemplate(tpl.slug)
    pushSnack(t('admin:templates.toast.deleted'), 'success')
    await load()
  }

  async function confirmReset(tpl: Template) {
    const ok = await confirm({
      title: t('admin:templates.confirm.reset_title'),
      message: t('admin:templates.confirm.reset_message', { slug: tpl.slug }),
      destructive: true,
    })
    if (!ok) return
    await resetTemplate(tpl.slug)
    pushSnack(t('admin:templates.toast.reset'), 'success')
    await load()
  }

  async function batchDelete() {
    const rows = items.filter(i => selected.has(i.slug))
    if (!rows.length) return
    const ok = await confirm({
      title: t('admin:templates.confirm.batch_delete_title'),
      message: t('admin:templates.confirm.batch_delete_message', { count: rows.length }),
      destructive: true,
    })
    if (!ok) return
    setBatchBusy(true)
    try {
      const results = await Promise.allSettled(rows.map(r => deleteTemplate(r.slug)))
      const okSlugs = rows.filter((_, i) => results[i].status === 'fulfilled').map(r => r.slug)
      const failed = rows.length - okSlugs.length
      setItems(prev => prev.filter(x => !okSlugs.includes(x.slug)))
      setSelected(new Set())
      if (failed > 0) pushSnack(t('admin:templates.toast.batch_partial', { ok: okSlugs.length, fail: failed }), 'warning')
      else pushSnack(t('admin:templates.toast.batch_deleted', { count: okSlugs.length }), 'success')
    } finally { setBatchBusy(false) }
  }

  function ruleSetSummary(tpl: Template): string {
    if (!tpl.rule_sets?.length) return t('admin:templates.status.no_binding')
    return tpl.rule_sets.map(s => ruleSetMap.get(s)?.name || s).join('、')
  }

  function coverageBadge(tpl: Template) {
    const sets = (tpl.rule_sets || []).map(s => ruleSetMap.get(s)).filter((x): x is RuleSet => Boolean(x))
    if (sets.length === 0) return badge(t('admin:templates.status.no_binding'), md.surfaceContainerHighest, md.onSurfaceVariant)
    if (usesDynamicProxyGroups(tpl.content)) return badge(t('admin:templates.status.auto_generated'), md.tertiaryContainer, md.onTertiaryContainer)
    const groups = extractProxyGroups(tpl.content)
    const missing = missingTargets(groups, extractRuleTargets(sets))
    if (missing.length > 0) return badge(t('admin:templates.status.missing_n', { n: missing.length }), md.errorContainer, md.onErrorContainer)
    return badge(t('admin:templates.status.ok'), md.tertiaryContainer, md.onTertiaryContainer)
  }

  function badge(label: string, bg: string, fg: string) {
    return <Box sx={{
      display: 'inline-block', px: 1.25, py: 0.25,
      borderRadius: 1, fontSize: 12, fontWeight: 500,
      bgcolor: bg, color: fg, whiteSpace: 'nowrap',
    }}>{label}</Box>
  }

  // Form analysis
  const formSelectedRuleSets = form.rule_sets.map(s => ruleSetMap.get(s)).filter((x): x is RuleSet => Boolean(x))
  const formProxyGroups = extractProxyGroups(form.content)
  const formRuleTargets = extractRuleTargets(formSelectedRuleSets)
  const formMissing = missingTargets(formProxyGroups, formRuleTargets)
  const formDynamic = usesDynamicProxyGroups(form.content)

  function formHint() {
    if (formDynamic && form.rule_sets.length > 0) {
      return { color: md.tertiary, text: t('admin:templates.hint.auto_groups') }
    }
    if (formMissing.length > 0 && form.content.length > 50) {
      return {
        color: md.error,
        text: t('admin:templates.hint.missing_targets', {
          names: formMissing.slice(0, 8).join('、') + (formMissing.length > 8 ? ` +${formMissing.length - 8}` : ''),
        }),
      }
    }
    if (form.rule_sets.length > 0 && formProxyGroups.size > 0) {
      return { color: md.tertiary, text: t('admin:templates.hint.groups_ok', { n: formProxyGroups.size }) }
    }
    if (form.rule_sets.length === 0) {
      return { color: md.onSurfaceVariant, text: t('admin:templates.hint.no_binding') }
    }
    if (form.content.length < 50) {
      return { color: md.onSurfaceVariant, text: t('admin:templates.hint.incomplete') }
    }
    return null
  }
  const hint = formHint()

  return (
    <Box sx={{ p: 3 }}>
      <PageHeader
        title={t('admin:templates.title')}
        subtitle={t('admin:templates.subtitle')}
        actions={canConfig && <Button variant="contained" startIcon={<AddIcon />} onClick={openCreate}>
          {t('admin:templates.create')}
        </Button>}
      />
      {selected.size > 0 && canConfig && (
        <Box sx={{
          display: 'flex', alignItems: 'center', gap: 1, mt: 2, mb: 1, px: 2, py: 1,
          borderRadius: 9999, bgcolor: md.secondaryContainer, color: md.onSecondaryContainer,
          width: 'fit-content',
        }}>
          <Typography sx={{ fontSize: 13, fontWeight: 500, mr: 1 }}>
            {t('admin:templates.selection_count', { count: selected.size })}
          </Typography>
          <Button size="small" variant="text" color="error"
            startIcon={batchBusy ? <CircularProgress size={14} /> : <DeleteIcon />}
            disabled={batchBusy} onClick={batchDelete}>
            {t('admin:templates.batch_delete')}
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
                    onChange={(_, c) => toggleAll(c)} disabled={selectableSlugs.length === 0} />
                </TableCell>
                <TableCell>{t('admin:templates.table.slug')}</TableCell>
                <TableCell>{t('admin:templates.table.name')}</TableCell>
                <TableCell>{t('admin:templates.table.client_type')}</TableCell>
                <TableCell>{t('admin:templates.table.rule_sets')}</TableCell>
                <TableCell>{t('admin:templates.table.coverage')}</TableCell>
                <TableCell>{t('admin:templates.table.default')}</TableCell>
                <TableCell align="right">{t('admin:templates.table.actions')}</TableCell>
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
              {pagedItems.map(tpl => (
                <TableRow key={tpl.slug} hover sx={{ '& td': { borderBottom: `1px solid ${md.outlineVariant}`, whiteSpace: 'nowrap' } }}>
                  <TableCell padding="checkbox">
                    <Checkbox checked={selected.has(tpl.slug)}
                      disabled={tpl.is_default || SEEDED_TEMPLATE_SLUGS.includes(tpl.slug)}
                      onChange={(_, c) => toggleOne(tpl.slug, c)} />
                  </TableCell>
                  <TableCell sx={{ fontSize: 13 }}>{tpl.slug}</TableCell>
                  <TableCell sx={{ fontWeight: 500 }}>{tpl.name}</TableCell>
                  <TableCell sx={{ fontSize: 13, color: md.onSurfaceVariant }}>{tpl.client_type}</TableCell>
                  <TableCell sx={{ fontSize: 13, maxWidth: 280, overflow: 'hidden', textOverflow: 'ellipsis' }}>
                    {ruleSetSummary(tpl)}
                  </TableCell>
                  <TableCell>{coverageBadge(tpl)}</TableCell>
                  <TableCell>
                    {tpl.is_default && badge(t('admin:templates.status.default'), md.primaryContainer, md.onPrimaryContainer)}
                  </TableCell>
                  <TableCell align="right">
                    {canConfig && <>
                    <Tooltip title={t('admin:templates.field.is_default')}>
                      <IconButton size="small" onClick={() => openEdit(tpl)}><EditIcon fontSize="small" /></IconButton>
                    </Tooltip>
                    <Tooltip title={t('admin:templates.duplicate')}>
                      <IconButton size="small" onClick={() => openDuplicate(tpl)}
                        sx={{ color: md.onSurfaceVariant }}>
                        <ContentCopyIcon fontSize="small" />
                      </IconButton>
                    </Tooltip>
                    {SEEDED_TEMPLATE_SLUGS.includes(tpl.slug) && (
                      <Tooltip title={t('admin:templates.reset_to_default')}>
                        <IconButton size="small" onClick={() => confirmReset(tpl)}
                          sx={{ color: md.onSurfaceVariant }}>
                          <RestartAltIcon fontSize="small" />
                        </IconButton>
                      </Tooltip>
                    )}
                    <Tooltip title={SEEDED_TEMPLATE_SLUGS.includes(tpl.slug)
                      ? t('admin:templates.cannot_delete_seeded')
                      : t('admin:templates.batch_delete')}>
                      <span>
                        <IconButton size="small" onClick={() => confirmDelete(tpl)}
                          disabled={tpl.is_default || SEEDED_TEMPLATE_SLUGS.includes(tpl.slug)}
                          sx={{ color: md.error }}>
                          <DeleteIcon fontSize="small" />
                        </IconButton>
                      </span>
                    </Tooltip>
                    </>}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </TableContainer>
        <PagedTableFooter
          total={items.length} page={page} pageSize={pageSize}
          onPageChange={setPage} onPageSizeChange={changePageSize}
        />
      </Card>
      {/* Create/Edit dialog */}
      <Dialog open={dialogOpen} onClose={() => !busy && setDialogOpen(false)}
        slotProps={{
          paper: { sx: { borderRadius: 3, bgcolor: md.surfaceContainerHigh, width: 900, maxWidth: '95vw' } }
        }}>
        <DialogTitle>
          {editing ? t('admin:templates.edit_title') : t('admin:templates.create')}
        </DialogTitle>
        <DialogContent>
          <Box component="form" id="tpl-form" onSubmit={submit} sx={{ display: 'flex', flexDirection: 'column', gap: 2.5, pt: 1 }}>
            <Box sx={{ display: 'flex', gap: 2 }}>
              <TextField required label={t('admin:templates.field.slug')}
                value={form.slug} disabled={editing}
                onChange={e => setForm({ ...form, slug: e.target.value })}
                sx={{ flex: 1, '& input': {  } }} />
              <TextField required label={t('admin:templates.field.name')}
                value={form.name} onChange={e => setForm({ ...form, name: e.target.value })}
                sx={{ flex: 1 }} />
            </Box>
            <Box sx={{ display: 'flex', gap: 2, alignItems: 'center' }}>
              <TextField select size="small" label={t('admin:templates.field.client_type')}
                value={form.client_type}
                onChange={e => setForm({ ...form, client_type: e.target.value })}
                sx={{ minWidth: 220 }}>
                <MenuItem value="mihomo">mihomo</MenuItem>
                <MenuItem value="sing-box">Sing-box</MenuItem>
              </TextField>
              <FormControlLabel
                label={t('admin:templates.field.is_default')}
                control={<Switch checked={form.is_default} onChange={(_, c) => setForm({ ...form, is_default: c })} />}
                sx={{ ml: 1, '& .MuiFormControlLabel-label': { ml: 1.5 } }}
              />
            </Box>
            <Box>
              <FormControl fullWidth size="small">
                <InputLabel shrink>{t('admin:templates.field.rule_sets')}</InputLabel>
                <Select multiple
                  value={form.rule_sets}
                  onChange={e => setForm({ ...form, rule_sets: e.target.value as string[] })}
                  input={<OutlinedInput notched label={t('admin:templates.field.rule_sets')} />}
                  displayEmpty
                  renderValue={(sel) =>
                    (sel as string[]).length === 0
                      ? <Typography sx={{ color: md.onSurfaceVariant, fontSize: 14 }}>{t('admin:templates.placeholder.rule_sets')}</Typography>
                      : (
                        <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 0.5 }}>
                          {(sel as string[]).map(s => {
                            const rs = ruleSetMap.get(s)
                            return <Chip key={s} label={rs?.name || s} size="small" />
                          })}
                        </Box>
                      )}
                >
                  {ruleSets.map(rs => (
                    <MenuItem key={rs.slug} value={rs.slug} disabled={!rs.enabled}>
                      <Checkbox checked={form.rule_sets.includes(rs.slug)} sx={{ mr: 1, p: 0.5 }} />
                      <ListItemText primary={`${rs.name} (${rs.slug})`} />
                    </MenuItem>
                  ))}
                </Select>
              </FormControl>
              <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, mt: 0.5 }}>
                {t('admin:templates.hint.rule_sets')}
              </Typography>
              {hint && (
                <Typography sx={{ fontSize: 12, color: hint.color, mt: 1 }}>{hint.text}</Typography>
              )}
            </Box>
            <TextField fullWidth multiline minRows={14} maxRows={28}
              label={t('admin:templates.field.content')}
              placeholder={t('admin:templates.placeholder.content')}
              value={form.content}
              onChange={e => setForm({ ...form, content: e.target.value })}
              sx={{ '& textarea': { fontSize: 13 } }} />
          </Box>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setDialogOpen(false)} disabled={busy} variant="text">{t('common:actions.cancel')}</Button>
          <Button type="submit" form="tpl-form" variant="contained" disabled={busy}
            startIcon={busy ? <CircularProgress size={16} color="inherit" /> : null}>
            {t('common:actions.ok')}
          </Button>
        </DialogActions>
      </Dialog>
    </Box>
  );
}
