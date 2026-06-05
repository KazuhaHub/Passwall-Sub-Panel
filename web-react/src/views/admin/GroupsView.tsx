import { useEffect, useMemo, useState, type FormEvent } from 'react'
import {
  Autocomplete,
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
  FormControlLabel,
  IconButton,
  InputAdornment,
  MenuItem,
  Switch,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  TextField,
  Typography,
  alpha,
  useTheme,
} from '@mui/material'
import AddIcon from '@mui/icons-material/Add'
import SearchIcon from '@mui/icons-material/Search'
import DeleteIcon from '@mui/icons-material/DeleteOutline'
import EditIcon from '@mui/icons-material/EditOutlined'
import { useTranslation } from 'react-i18next'
import { useCan } from '@/utils/permissions'

import { createGroup, deleteGroup, listGroups, updateGroup } from '@/api/groups'
import { listNodes } from '@/api/nodes'
import type { Group, Node } from '@/api/types'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'
import { PagedTableFooter } from '@/components/PagedTableFooter'
import PageHeader from '@/components/PageHeader'

interface FormState {
  slug: string
  name: string
  all: boolean
  mode: 'all' | 'any'   // AND vs OR over tags
  // Conditions are split by prefix at edit time so the UI can render
  // three dedicated multi-select fields, then rejoined on submit into the
  // backend's `tag_filter.tags []string` shape ("region:US", "tag:Premium",
  // "server:1.1.1.1"). Anything that doesn't match a known prefix lives in
  // `custom_text` so admins running advanced conditions (e.g. "vendor:gcp"
  // stored as a literal tag) can still round-trip them.
  regions: string[]
  tags: string[]
  servers: string[]
  custom_text: string
  remark: string
}

const EMPTY_FORM: FormState = {
  slug: '', name: '', all: false, mode: 'all',
  regions: [], tags: [], servers: [], custom_text: '',
  remark: '',
}

// parseTagConditions splits a stored tag_filter.tags array into the four
// buckets the UI exposes. The backend's matchOne dispatch handles
// region:/tag:/node: prefixes specially, anything else (including bare
// strings) is treated as a literal tag lookup — see internal/service/group.
//
// `server:` is the legacy spelling of `node:` (renamed in v3.0.0-beta.11
// when the UI label moved from "Server" to "Node"). Existing stored
// conditions read into the same bucket; on save they're rewritten to the
// new prefix so the legacy form drains out naturally.
function parseTagConditions(conds: string[]): {
  regions: string[]
  tags: string[]
  servers: string[]
  custom: string[]
} {
  const out = { regions: [] as string[], tags: [] as string[], servers: [] as string[], custom: [] as string[] }
  for (const c of conds) {
    const s = c.trim()
    if (!s) continue
    const i = s.indexOf(':')
    if (i > 0) {
      const key = s.slice(0, i).trim()
      const val = s.slice(i + 1).trim()
      if (key === 'region') { out.regions.push(val); continue }
      if (key === 'tag') { out.tags.push(val); continue }
      if (key === 'node' || key === 'server') { out.servers.push(val); continue }
    }
    out.custom.push(s)
  }
  return out
}

// buildTagConditions packages the four buckets back into the backend's
// flat conditions array. Empty/whitespace-only entries are dropped.
function buildTagConditions(f: FormState): string[] {
  const conds: string[] = []
  for (const r of f.regions) { const v = r.trim(); if (v) conds.push(`region:${v}`) }
  for (const tg of f.tags) { const v = tg.trim(); if (v) conds.push(`tag:${v}`) }
  for (const sv of f.servers) { const v = sv.trim(); if (v) conds.push(`node:${v}`) }
  for (const raw of f.custom_text.split(',')) {
    const v = raw.trim()
    if (v) conds.push(v)
  }
  return conds
}

// MultiPicker is the shared multi-select Autocomplete for the
// region / tag / server fields in the group's tag_filter editor.
// freeSolo so admins can introduce a value that doesn't appear in the
// dropdown yet (e.g. a region they're about to use on a new node).
function MultiPicker(props: {
  label: string
  options: string[]
  value: string[]
  onChange: (next: string[]) => void
}) {
  return (
    <Autocomplete
      multiple
      freeSolo
      options={props.options}
      value={props.value}
      onChange={(_, v) => {
        const seen = new Set<string>()
        const cleaned: string[] = []
        for (const raw of v as string[]) {
          const s = raw.trim()
          if (!s || seen.has(s)) continue
          seen.add(s)
          cleaned.push(s)
        }
        props.onChange(cleaned)
      }}
      renderTags={(value, getTagProps) =>
        value.map((option, index) => {
          const tagProps = getTagProps({ index })
          return <Chip {...tagProps} key={option} label={option} size="small" />
        })
      }
      renderInput={(params) => (
        <TextField {...params} label={props.label} />
      )}
    />
  )
}

export default function GroupsView() {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation(['admin', 'common'])
  const canConfig = useCan('config.write')

  const [items, setItems] = useState<Group[]>([])
  const [search, setSearch] = useState('')
  const [loading, setLoading] = useState(false)
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [batchBusy, setBatchBusy] = useState(false)

  const [dialogOpen, setDialogOpen] = useState(false)
  const [editing, setEditing] = useState<Group | null>(null)
  const [form, setForm] = useState<FormState>(EMPTY_FORM)
  const [busy, setBusy] = useState(false)

  // Tag filter conditions accept "region:XX" / "tag:YY" / a bare tag. Build
  // the dropdown suggestions by scanning every managed node — regions get a
  // `region:` prefix, tags get a `tag:` prefix so admins discover both forms
  // and the matcher's special-key dispatch works as expected. The Autocomplete
  // stays freeSolo so admins can still type custom conditions (e.g. a tag
  // that doesn't exist yet but will after they save it on a node).
  const [allNodes, setAllNodes] = useState<Node[]>([])
  useEffect(() => {
    void listNodes({ page: 1, page_size: 500 })
      .then(res => setAllNodes(res.items))
      .catch(() => { /* leave empty */ })
  }, [])
  // Separate option pools per dropdown — the UI splits the prefix-based
  // conditions into three dedicated fields (Region / Tag / Server) so
  // admins don't have to remember the "key:value" syntax. Backend payload
  // is still a flat conditions array; buildTagConditions reassembles it.
  const regionOptions = useMemo(() => {
    const s = new Set<string>()
    for (const n of allNodes) if (n.region) s.add(n.region)
    return Array.from(s).sort((a, b) => a.localeCompare(b))
  }, [allNodes])
  const tagOptions = useMemo(() => {
    const s = new Set<string>()
    for (const n of allNodes) for (const tg of n.tags ?? []) if (tg) s.add(tg)
    return Array.from(s).sort((a, b) => a.localeCompare(b))
  }, [allNodes])
  const serverOptions = useMemo(() => {
    const s = new Set<string>()
    for (const n of allNodes) if (n.server_address) s.add(n.server_address)
    return Array.from(s).sort((a, b) => a.localeCompare(b))
  }, [allNodes])

  // Free-text filter on name / slug (the two identifiers). Remark is left
  // out — it's a human-readable note, not something to match on.
  const filteredItems = useMemo(() => {
    const q = search.trim().toLowerCase()
    if (!q) return items
    return items.filter(g =>
      g.name.toLowerCase().includes(q) ||
      g.slug.toLowerCase().includes(q),
    )
  }, [items, search])

  // Client-side pagination — group lists are tiny but the footer keeps
  // the UX consistent across every admin table.
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
  useEffect(() => { setPage(1) }, [search])
  const pagedItems = useMemo(
    () => filteredItems.slice((page - 1) * pageSize, page * pageSize),
    [filteredItems, page, pageSize],
  )

  // Only groups with zero members are eligible for selection (delete needs
  // empty group). Scoped to the CURRENTLY VISIBLE page (pagedItems) so the
  // header checkbox + toggleAll act on rows admin can actually see. Pre-
  // beta.5 this used filteredItems (the full filtered set), which meant
  // header "select all" silently flipped rows on hidden pages.
  const selectableIds = pagedItems.filter(g => g.members === 0).map(g => g.id)
  const selectedCount = selected.size
  const allChecked = selectableIds.length > 0 && selectableIds.every(id => selected.has(id))
  const someChecked = selectableIds.some(id => selected.has(id)) && !allChecked

  useEffect(() => { void load() }, [])

  async function load() {
    setLoading(true)
    try {
      const res = await listGroups()
      setItems(res.items)
      setSelected(new Set())
    } finally {
      setLoading(false)
    }
  }

  function openCreate() {
    setEditing(null)
    setForm(EMPTY_FORM)
    setDialogOpen(true)
  }

  function openEdit(g: Group) {
    setEditing(g)
    const parsed = parseTagConditions(g.tag_filter.tags || [])
    setForm({
      slug: g.slug,
      name: g.name,
      all: g.tag_filter.all,
      mode: g.tag_filter.mode === 'any' ? 'any' : 'all',
      regions: parsed.regions,
      tags: parsed.tags,
      servers: parsed.servers,
      custom_text: parsed.custom.join(', '),
      remark: g.remark || '',
    })
    setDialogOpen(true)
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (!editing && !form.slug) { pushSnack(t('admin:groups.validate.slug_required'), 'warning'); return }
    if (!form.name) { pushSnack(t('admin:groups.validate.name_required'), 'warning'); return }
    setBusy(true)
    try {
      const tagFilter = {
        all: form.all,
        // Send "all" / "any" — backend treats empty as "all", but being
        // explicit makes the wire shape self-describing.
        mode: form.mode,
        tags: form.all ? [] : buildTagConditions(form),
      }
      if (editing) {
        const res = await updateGroup(editing.id, {
          name: form.name,
          tag_filter: tagFilter,
          remark: form.remark,
        })
        pushSnack(t('admin:groups.toast.updated'), 'success')
        if (res.resync_errors?.length) {
          pushSnack(t('admin:groups.toast.resync_partial', { count: res.resync_errors.length }), 'warning')
        }
      } else {
        await createGroup({
          slug: form.slug,
          name: form.name,
          tag_filter: tagFilter,
          remark: form.remark,
        })
        pushSnack(t('admin:groups.toast.created'), 'success')
      }
      setDialogOpen(false)
      await load()
    } finally {
      setBusy(false)
    }
  }

  async function confirmDelete(g: Group) {
    if (g.members > 0) {
      pushSnack(t('admin:groups.warn.has_members', { count: g.members }), 'warning')
      return
    }
    const ok = await confirm({
      title: t('admin:groups.confirm.delete_title'),
      message: t('admin:groups.confirm.delete_message', { name: g.name }),
      destructive: true,
      confirmText: t('admin:groups.action.delete'),
    })
    if (!ok) return
    await deleteGroup(g.id)
    pushSnack(t('admin:groups.toast.deleted'), 'success')
    await load()
  }

  async function batchDeleteGroups() {
    const rows = items.filter(g => selected.has(g.id))
    if (!rows.length) return
    const names = rows.slice(0, 5).map(r => r.name).join('、')
    const suffix = rows.length > 5 ? ` +${rows.length - 5}` : ''
    const ok = await confirm({
      title: t('admin:groups.confirm.batch_delete_title'),
      message: t('admin:groups.confirm.batch_delete_message', { names, suffix }),
      destructive: true,
      confirmText: t('admin:groups.action.delete'),
    })
    if (!ok) return
    setBatchBusy(true)
    try {
      const results = await Promise.allSettled(rows.map(r => deleteGroup(r.id)))
      const okIds = rows.filter((_, i) => results[i].status === 'fulfilled').map(r => r.id)
      const failed = rows.length - okIds.length
      setItems(prev => prev.filter(g => !okIds.includes(g.id)))
      setSelected(new Set())
      if (failed > 0) {
        pushSnack(t('admin:groups.toast.batch_partial', { ok: okIds.length, fail: failed }), 'warning')
      } else {
        pushSnack(t('admin:groups.toast.batch_deleted', { count: okIds.length }), 'success')
      }
    } finally {
      setBatchBusy(false)
    }
  }

  function toggleAll(checked: boolean) {
    // Flip only the visible selectable rows; preserve selection of rows
    // hidden by the active search filter.
    setSelected(prev => {
      const next = new Set(prev)
      selectableIds.forEach(id => { if (checked) next.add(id); else next.delete(id) })
      return next
    })
  }

  function toggleOne(id: number, checked: boolean) {
    setSelected(prev => {
      const next = new Set(prev)
      if (checked) next.add(id); else next.delete(id)
      return next
    })
  }

  function tagFilterCell(g: Group) {
    if (g.tag_filter.all) {
      return (
        <Box sx={{
          display: 'inline-block', px: 1.25, py: 0.25,
          borderRadius: 1, fontSize: 12, fontWeight: 500,
          bgcolor: md.tertiaryContainer, color: md.onTertiaryContainer,
        }}>
          {t('admin:groups.tag.all')}
        </Box>
      )
    }
    if (!g.tag_filter.tags?.length) {
      return <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant }}>—</Typography>
    }
    // Render the mode (AND / OR) as a small badge before the tags so the
    // admin sees at a glance whether the conditions are conjunctive or
    // disjunctive. Defaults to AND for rows persisted before the field
    // existed.
    const isAny = g.tag_filter.mode === 'any'
    return (
      <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 0.5, alignItems: 'center' }}>
        <Box sx={{
          display: 'inline-block', px: 1.25, py: 0.25,
          borderRadius: 1, fontSize: 11, fontWeight: 600, letterSpacing: '.5px',
          bgcolor: isAny ? md.secondaryContainer : md.primaryContainer,
          color: isAny ? md.onSecondaryContainer : md.onPrimaryContainer,
        }}>
          {isAny ? t('admin:groups.mode.any_badge') : t('admin:groups.mode.all_badge')}
        </Box>
        {g.tag_filter.tags.map(tag => (
          <Box key={tag} sx={{
            display: 'inline-block', px: 1.25, py: 0.25,
            borderRadius: 1, fontSize: 12, fontWeight: 500,
            bgcolor: md.surfaceContainerHighest, color: md.onSurfaceVariant,
          }}>
            {tag}
          </Box>
        ))}
      </Box>
    )
  }

  return (
    <Box sx={{ p: 3 }}>
      <PageHeader
        title={t('admin:groups.title')}
        actions={canConfig && <Button variant="contained" startIcon={<AddIcon />} onClick={openCreate}>
          {t('admin:groups.create')}
        </Button>}
      />

      {selectedCount > 0 && canConfig && (
        <Box sx={{
          display: 'flex', alignItems: 'center', gap: 1, mt: 2, mb: 1,
          px: 2, py: 1, borderRadius: 9999,
          bgcolor: md.secondaryContainer, color: md.onSecondaryContainer,
          width: 'fit-content',
        }}>
          <Typography sx={{ fontSize: 13, fontWeight: 500, mr: 1 }}>
            {t('admin:groups.selection_count', { count: selectedCount })}
          </Typography>
          <Button
            size="small" variant="text" color="error"
            startIcon={batchBusy ? <CircularProgress size={14} /> : <DeleteIcon />}
            disabled={batchBusy}
            onClick={batchDeleteGroups}
          >
            {t('admin:groups.batch_delete')}
          </Button>
        </Box>
      )}

      <Box sx={{ mt: 2 }}>
        <TextField
          size="small"
          value={search}
          onChange={e => setSearch(e.target.value)}
          placeholder={t('admin:groups.search_placeholder', { defaultValue: '搜索名称 / slug' })}
          sx={{ width: 320, maxWidth: '100%' }}
          InputProps={{
            startAdornment: (
              <InputAdornment position="start">
                <SearchIcon fontSize="small" sx={{ color: md.onSurfaceVariant }} />
              </InputAdornment>
            ),
          }}
        />
      </Box>

      <Card sx={{ mt: 2, bgcolor: md.surfaceContainerLow, boxShadow: '0 1px 2px rgba(0,0,0,.3),0 1px 3px 1px rgba(0,0,0,.15)', overflow: 'hidden' }}>
        <TableContainer>
          <Table>
            <TableHead>
              <TableRow sx={{ '& th': { color: md.onSurfaceVariant, fontWeight: 500, fontSize: 12, textTransform: 'uppercase', letterSpacing: '.5px', borderBottom: `1px solid ${md.outlineVariant}` } }}>
                <TableCell padding="checkbox">
                  <Checkbox
                    indeterminate={someChecked}
                    checked={allChecked}
                    onChange={(_, c) => toggleAll(c)}
                    disabled={selectableIds.length === 0}
                  />
                </TableCell>
                <TableCell>{t('admin:groups.table.name')}</TableCell>
                <TableCell>{t('admin:groups.table.slug')}</TableCell>
                <TableCell>{t('admin:groups.table.tag_filter')}</TableCell>
                <TableCell align="right">{t('admin:groups.table.members')}</TableCell>
                <TableCell>{t('admin:groups.table.remark')}</TableCell>
                <TableCell align="right">{t('admin:groups.table.actions')}</TableCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {loading && items.length === 0 && (
                <TableRow><TableCell colSpan={7} sx={{ textAlign: 'center', py: 6 }}>
                  <CircularProgress size={24} />
                </TableCell></TableRow>
              )}
              {!loading && items.length === 0 && (
                <TableRow><TableCell colSpan={7} sx={{ textAlign: 'center', py: 6, color: md.onSurfaceVariant }}>
                  —
                </TableCell></TableRow>
              )}
              {!loading && items.length > 0 && filteredItems.length === 0 && (
                <TableRow><TableCell colSpan={7} sx={{ textAlign: 'center', py: 6, color: md.onSurfaceVariant }}>
                  {t('admin:groups.no_match', { defaultValue: '没有匹配的分组' })}
                </TableCell></TableRow>
              )}
              {pagedItems.map(g => {
                const canSelect = g.members === 0
                return (
                  <TableRow
                    key={g.id}
                    hover
                    sx={{ '& td': { borderBottom: `1px solid ${md.outlineVariant}` } }}
                  >
                    <TableCell padding="checkbox">
                      <Checkbox
                        checked={selected.has(g.id)}
                        onChange={(_, c) => toggleOne(g.id, c)}
                        disabled={!canSelect}
                      />
                    </TableCell>
                    <TableCell sx={{ fontWeight: 500 }}>{g.name}</TableCell>
                    <TableCell sx={{ fontSize: 13, color: md.onSurfaceVariant }}>{g.slug}</TableCell>
                    <TableCell>{tagFilterCell(g)}</TableCell>
                    <TableCell align="right" sx={{ fontVariantNumeric: 'tabular-nums' }}>{g.members}</TableCell>
                    <TableCell sx={{ color: md.onSurfaceVariant, fontSize: 13 }}>{g.remark || '—'}</TableCell>
                    <TableCell align="right" sx={{ whiteSpace: 'nowrap' }}>
                      {canConfig && <>
                        <IconButton size="small" onClick={() => openEdit(g)} aria-label={t('admin:groups.action.edit')}>
                          <EditIcon fontSize="small" />
                        </IconButton>
                        <IconButton
                          size="small"
                          onClick={() => confirmDelete(g)}
                          aria-label={t('admin:groups.action.delete')}
                          sx={{ color: md.error, '&.Mui-disabled': { color: alpha(md.error, 0.4) } }}
                        >
                          <DeleteIcon fontSize="small" />
                        </IconButton>
                      </>}
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        </TableContainer>
        <PagedTableFooter
          total={filteredItems.length} page={page} pageSize={pageSize}
          onPageChange={setPage} onPageSizeChange={changePageSize}
        />
      </Card>

      <Dialog
        open={dialogOpen}
        onClose={() => !busy && setDialogOpen(false)}
        PaperProps={{ sx: { borderRadius: 3, bgcolor: md.surfaceContainerHigh, width: 500, maxWidth: '90vw' } }}
      >
        <DialogTitle>
          {editing ? t('admin:groups.edit_title') : t('admin:groups.create')}
        </DialogTitle>
        <DialogContent>
          <Box component="form" id="group-form" onSubmit={submit} sx={{ display: 'flex', flexDirection: 'column', gap: 2.5, pt: 1 }}>
            <TextField
              fullWidth required
              label={t('admin:groups.field.slug')}
              value={form.slug}
              disabled={!!editing}
              onChange={e => setForm({ ...form, slug: e.target.value })}
              sx={{ '& input': {  } }}
            />
            <TextField
              fullWidth required
              label={t('admin:groups.field.name')}
              value={form.name}
              onChange={e => setForm({ ...form, name: e.target.value })}
            />
            <FormControlLabel
              label={t('admin:groups.field.match_all')}
              control={
                <Switch checked={form.all} onChange={(_, c) => setForm({ ...form, all: c })} />
              }
              sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 1.5 } }}
            />
            {!form.all && (
              <>
                <TextField
                  select
                  fullWidth
                  label={t('admin:groups.field.mode')}
                  value={form.mode}
                  onChange={e => setForm({ ...form, mode: e.target.value as 'all' | 'any' })}
                  helperText={t('admin:groups.hint.mode')}
                >
                  <MenuItem value="all">{t('admin:groups.mode.all')}</MenuItem>
                  <MenuItem value="any">{t('admin:groups.mode.any')}</MenuItem>
                </TextField>
                <MultiPicker
                  label={t('admin:groups.field.region')}
                  options={regionOptions}
                  value={form.regions}
                  onChange={v => setForm({ ...form, regions: v })}
                />
                <MultiPicker
                  label={t('admin:groups.field.tag')}
                  options={tagOptions}
                  value={form.tags}
                  onChange={v => setForm({ ...form, tags: v })}
                />
                <MultiPicker
                  label={t('admin:groups.field.node')}
                  options={serverOptions}
                  value={form.servers}
                  onChange={v => setForm({ ...form, servers: v })}
                />
                <TextField
                  fullWidth
                  label={t('admin:groups.field.custom_conditions')}
                  placeholder="vendor:gcp, ..."
                  helperText={t('admin:groups.hint.custom_conditions')}
                  value={form.custom_text}
                  onChange={e => setForm({ ...form, custom_text: e.target.value })}
                />
              </>
            )}
            <TextField
              fullWidth
              label={t('admin:groups.field.remark')}
              value={form.remark}
              onChange={e => setForm({ ...form, remark: e.target.value })}
            />
          </Box>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setDialogOpen(false)} disabled={busy} variant="text">
            {t('common:actions.cancel')}
          </Button>
          <Button
            type="submit" form="group-form"
            variant="contained" disabled={busy}
            startIcon={busy ? <CircularProgress size={16} color="inherit" /> : null}
          >
            {t('common:actions.ok')}
          </Button>
        </DialogActions>
      </Dialog>
    </Box>
  )
}
