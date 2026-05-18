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
import DeleteIcon from '@mui/icons-material/DeleteOutline'
import EditIcon from '@mui/icons-material/EditOutlined'
import { useTranslation } from 'react-i18next'

import { createGroup, deleteGroup, listGroups, updateGroup } from '@/api/groups'
import { listNodes } from '@/api/nodes'
import type { Group, Node } from '@/api/types'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'

interface FormState {
  slug: string
  name: string
  all: boolean
  mode: 'all' | 'any'   // AND vs OR over tags
  tags_text: string
  remark: string
}

const EMPTY_FORM: FormState = {
  slug: '', name: '', all: false, mode: 'all', tags_text: '', remark: '',
}

export default function GroupsView() {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation(['admin', 'common'])

  const [items, setItems] = useState<Group[]>([])
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
    void listNodes().then(setAllNodes).catch(() => { /* leave empty */ })
  }, [])
  const tagFilterOptions = useMemo(() => {
    const regions = new Set<string>()
    const tags = new Set<string>()
    for (const n of allNodes) {
      if (n.region) regions.add(n.region)
      for (const tg of n.tags ?? []) if (tg) tags.add(tg)
    }
    const out: string[] = []
    for (const r of Array.from(regions).sort()) out.push(`region:${r}`)
    for (const tg of Array.from(tags).sort()) out.push(`tag:${tg}`)
    return out
  }, [allNodes])

  // Only groups with zero members are eligible for selection (delete needs empty group).
  const selectableIds = items.filter(g => g.members === 0).map(g => g.id)
  const selectedCount = selected.size
  const allChecked = selectableIds.length > 0 && selectableIds.every(id => selected.has(id))
  const someChecked = selected.size > 0 && !allChecked

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
    setForm({
      slug: g.slug,
      name: g.name,
      all: g.tag_filter.all,
      mode: g.tag_filter.mode === 'any' ? 'any' : 'all',
      tags_text: (g.tag_filter.tags || []).join(', '),
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
        tags: form.all
          ? []
          : form.tags_text.split(',').map(s => s.trim()).filter(Boolean),
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
    setSelected(checked ? new Set(selectableIds) : new Set())
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
      <Box sx={{ display: 'flex', alignItems: 'flex-end', justifyContent: 'space-between', flexWrap: 'wrap', gap: 2, mb: 1 }}>
        <Typography variant="h4">{t('admin:groups.title')}</Typography>
        <Button variant="contained" startIcon={<AddIcon />} onClick={openCreate}>
          {t('admin:groups.create')}
        </Button>
      </Box>

      {selectedCount > 0 && (
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
              {items.map(g => {
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
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        </TableContainer>
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
                <Autocomplete
                  multiple
                  freeSolo
                  options={tagFilterOptions}
                  value={form.tags_text
                    ? form.tags_text.split(',').map(s => s.trim()).filter(Boolean)
                    : []}
                  onChange={(_, v) => {
                    const seen = new Set<string>()
                    const cleaned: string[] = []
                    for (const raw of v as string[]) {
                      const s = raw.trim()
                      if (!s || seen.has(s)) continue
                      seen.add(s)
                      cleaned.push(s)
                    }
                    setForm({ ...form, tags_text: cleaned.join(', ') })
                  }}
                  renderTags={(value, getTagProps) =>
                    value.map((option, index) => {
                      const tagProps = getTagProps({ index })
                      return <Chip {...tagProps} key={option} label={option} size="small" />
                    })
                  }
                  renderInput={(params) => (
                    <TextField
                      {...params}
                      label={t('admin:groups.field.tags')}
                      placeholder={t('admin:groups.placeholder.tags')}
                      helperText={t('admin:groups.hint.tags')}
                    />
                  )}
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
