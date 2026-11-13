import { useEffect, useMemo, useRef, useState } from 'react'
import {
  Alert, Box, Button, Dialog, DialogActions, DialogContent, DialogTitle, LinearProgress, Link, Menu, MenuItem, Paper,
  Skeleton, Table, TableBody, TableCell, TableContainer, TableHead, TableRow, Typography, useMediaQuery, useTheme,
} from '@mui/material'
import ArrowDropDownIcon from '@mui/icons-material/ArrowDropDown'
import { useLocation, useNavigate, useSearchParams } from 'react-router'
import { useTranslation } from 'react-i18next'

import { destErrorOf, isDestUnavailable, type DestBudgetItem, type DestList, type DestListKind, type DestPolicyRef } from '@/api/accessControl'
import { AsyncButton } from '@/components/AsyncButton'
import { confirm } from '@/components/ConfirmHost'
import KpiTile, { KpiGrid } from '@/components/KpiTile'
import { pushSnack } from '@/components/SnackbarHost'
import { SortableTableCell } from '@/components/SortableTableCell'
import { useDeleteDestList, useDestLists, useDestPolicies, useGeositeStatus, useRefreshDestList, useUpdateGeosite } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { budgetLevel } from '@/utils/accessControl'
import { parseListKindFilter, parseListStateFilter, type AccessNavState, type SettingsFocus } from '../accessParams'
import { deleteListConfirm } from '../confirmCopy'
import { destErrorText, reportDestError } from '../destErrors'
import ListDialog from './ListDialog'
import ListRow from './ListRow'
import { listStatus } from './listStatus'

const L = 'admin:access_control.lists.'

export interface ListsTabProps {
  /** Opens the entries sheet (`sheet=list&list=<id>`). */
  onOpenEntries: (id: number) => void
  /** Opens the settings dialog, focused on a field. */
  onOpenSettings: (focus?: SettingsFocus) => void
  /** Switches to the policies tab and opens that policy's editor. */
  onOpenPolicy: (id: number) => void
}

type SortColumn = 'name' | 'entries' | 'status'

/**
 * THE LISTS TAB (S6): how much of each quota the lists use, which list is
 * broken (and still serving its old content), and who uses each one. The
 * filters are in the URL (`lst_state`, `lst_kind`); sorting is the page's own.
 */
export default function ListsTab({ onOpenEntries, onOpenSettings, onOpenPolicy }: ListsTabProps) {
  const { t, i18n } = useTranslation(['admin'])
  const theme = useTheme()
  const md = theme.palette.md
  const folded = useMediaQuery(theme.breakpoints.down('md'))
  const scope = useQueryScope()
  const location = useLocation()
  const navigate = useNavigate()
  const [params, setParams] = useSearchParams()
  const lists = useDestLists(scope)
  const policies = useDestPolicies(scope)
  const del = useDeleteDestList(scope)
  const refresh = useRefreshDestList(scope)
  const download = useUpdateGeosite(scope)
  const geosite = useGeositeStatus(scope)
  const stateFilter = parseListStateFilter(params.get('lst_state'))
  const kindFilter = parseListKindFilter(params.get('lst_kind'))
  const [sort, setSort] = useState<{ col: SortColumn; dir: 'asc' | 'desc' }>({ col: 'name', dir: 'asc' })
  const [menu, setMenu] = useState<HTMLElement | null>(null)
  const [dialog, setDialog] = useState<{ open: boolean; editId: number | null; kind: DestListKind }>({ open: false, editId: null, kind: 'custom' })
  const [inUse, setInUse] = useState<{ name: string; usedBy: DestPolicyRef[] } | null>(null)

  // Another part of the page asked for a dialog on arrival (state, never the URL): do it once, clear it.
  // Keyed by the history entry, so a second request (a new entry) is handled too.
  const handled = useRef<string | null>(null)
  useEffect(() => {
    const want = (location.state as AccessNavState | null)?.openList
    if (want === undefined || handled.current === location.key) return
    handled.current = location.key
    if (typeof want === 'number') setDialog({ open: true, editId: want, kind: 'custom' })
    else setDialog({ open: true, editId: null, kind: want.replace('new_', '') as DestListKind })
    void navigate({ pathname: location.pathname, search: location.search }, { replace: true, state: null })
  }, [location, navigate])

  const enabledIds = useMemo(
    () => new Set([...(policies.data?.allow ?? []), ...(policies.data?.block ?? [])].filter(p => p.state === 'on').map(p => p.id)),
    [policies.data])
  const items = lists.data?.items
  const usedEnabled = (l: DestList) => l.used_by.some(u => enabledIds.has(u.id))
  const problems = (items ?? []).filter(l => listStatus(l, usedEnabled(l)).problem).length

  const visible = useMemo(() => {
    let out = (items ?? []).filter(l => !kindFilter || l.kind === kindFilter)
    if (stateFilter === 'problem') out = out.filter(l => listStatus(l, l.used_by.some(u => enabledIds.has(u.id))).problem)
    const sign = sort.dir === 'asc' ? 1 : -1
    return [...out].sort((a, b) => {
      switch (sort.col) {
        case 'entries': return sign * (a.entry_count - b.entry_count)
        case 'status': return sign * ((a.last_fetched_at ?? 0) - (b.last_fetched_at ?? 0))
        default: return sign * a.name.localeCompare(b.name, i18n.language)
      }
    })
  }, [items, kindFilter, stateFilter, enabledIds, sort, i18n.language])

  const setFilter = (key: string, value: string | null) => setParams(prev => {
    const out = new URLSearchParams(prev)
    if (value) out.set(key, value)
    else out.delete(key)
    return out
  }, { replace: true })

  const onSort = (col: string) => setSort(s => ({ col: col as SortColumn, dir: s.col === col && s.dir === 'asc' ? 'desc' : 'asc' }))

  const doDelete = async (list: DestList) => {
    if (!(await confirm(deleteListConfirm(t, list.name)))) return
    try {
      await del.mutateAsync(list.id)
      pushSnack(t(`${L}snack.deleted`, { name: list.name }), 'success')
    } catch (err) {
      const e = destErrorOf(err)
      if (e?.code === 'dest_list_in_use') setInUse({ name: list.name, usedBy: (e.body.used_by as DestPolicyRef[] | undefined) ?? list.used_by })
      else reportDestError(t, err)
    }
  }
  const doRefresh = async (list: DestList) => {
    try {
      await refresh.mutateAsync(list.id)
      pushSnack(t(`${L}snack.refresh_started`, { name: list.name }), 'info')
    } catch (err) { reportDestError(t, err) }
  }

  const num = new Intl.NumberFormat(i18n.language)
  const tile = (label: string, b: DestBudgetItem | undefined) => {
    const level = budgetLevel(b)
    const color = level === 'over' ? md.error : undefined
    return (
      <KpiTile label={label} value={b ? num.format(b.used) : '—'} color={color}
        caption={b ? <>{t(`${L}kpi.of`, { limit: num.format(b.limit) })}{level === 'near' && <Box component="span" data-level="near" sx={{ color: theme.palette.warning.dark }}> ▲</Box>}</> : undefined} />
    )
  }
  const overQuota = lists.data ? (Object.values(lists.data.budget) as DestBudgetItem[]).some(b => budgetLevel(b) === 'over') : false

  const newMenu = (
    <>
      <Button variant="contained" endIcon={<ArrowDropDownIcon />} onClick={e => setMenu(e.currentTarget)}>{t(`${L}new`)}</Button>
      <Menu anchorEl={menu} open={menu !== null} onClose={() => setMenu(null)}>
        {(['custom', 'remote', 'geosite'] as const).map(k => (
          <MenuItem key={k} onClick={() => { setMenu(null); setDialog({ open: true, editId: null, kind: k }) }}>{t(`${L}kind.${k}`)}</MenuItem>
        ))}
      </Menu>
    </>
  )

  const unwired = lists.isError && isDestUnavailable(lists.error)
  let body
  if (lists.isPending) {
    body = <Box sx={{ display: 'grid', gap: 1 }}>{[0, 1, 2, 3, 4].map(i => <Skeleton key={i} variant="rounded" height={56} />)}</Box>
  } else if (lists.isError) {
    body = unwired
      ? <Alert severity="info">{t('admin:access_control.unwired')}</Alert>
      : <Alert severity="error" action={<Button color="inherit" size="small" onClick={() => void lists.refetch()}>{t('admin:access_control.retry')}</Button>}>{destErrorText(t, lists.error)}</Alert>
  } else if ((items ?? []).length === 0) {
    body = (
      <Box sx={{ p: 3, textAlign: 'center', border: `1px dashed ${md.outlineVariant}`, borderRadius: 3 }}>
        <Typography sx={{ mb: 1.5 }}>{t(`${L}empty`)}</Typography>
        {newMenu}
      </Box>
    )
  } else if (visible.length === 0) {
    body = (
      <Box sx={{ p: 3, textAlign: 'center' }}>
        <Typography sx={{ mb: 1 }}>{t(`${L}empty_filtered`)}</Typography>
        <Button onClick={() => setParams(prev => { const o = new URLSearchParams(prev); o.delete('lst_state'); o.delete('lst_kind'); return o }, { replace: true })}>
          {t(`${L}clear_filter`)}
        </Button>
      </Box>
    )
  } else {
    const rowProps = (l: DestList) => ({
      list: l, usedByEnabled: usedEnabled(l), onOpenEntries, onEdit: (id: number) => setDialog({ open: true, editId: id, kind: 'custom' }),
      onRefresh: doRefresh, onDelete: doDelete, onOpenPolicy,
    })
    body = folded
      ? <Box sx={{ display: 'grid', gap: 1 }}>{visible.map(l => <ListRow key={l.id} block {...rowProps(l)} />)}</Box>
      : (
        <TableContainer component={Paper} variant="outlined" sx={{ bgcolor: md.surfaceContainerLow, borderRadius: 3 }}>
          <Table size="small">
            <TableHead>
              <TableRow>
                <SortableTableCell column="name" activeColumn={sort.col} activeDir={sort.dir} onSort={onSort}>{t(`${L}cols.name`)}</SortableTableCell>
                <SortableTableCell column="entries" align="right" activeColumn={sort.col} activeDir={sort.dir} onSort={onSort}>{t(`${L}cols.entries`)}</SortableTableCell>
                <SortableTableCell column="status" activeColumn={sort.col} activeDir={sort.dir} onSort={onSort}>{t(`${L}cols.status`)}</SortableTableCell>
                <TableCell>{t(`${L}cols.usage`)}</TableCell>
                <TableCell align="right"><Box component="span" sx={{ position: 'absolute', width: 1, height: 1, overflow: 'hidden', clip: 'rect(0 0 0 0)' }}>{t(`${L}cols.actions`)}</Box></TableCell>
              </TableRow>
            </TableHead>
            <TableBody>{visible.map(l => <ListRow key={l.id} {...rowProps(l)} />)}</TableBody>
          </Table>
        </TableContainer>
      )
  }

  return (
    <Box>
      <Box sx={{ display: 'flex', justifyContent: 'flex-end', mb: 1 }}>{(items ?? []).length > 0 && newMenu}</Box>
      {lists.data && (
        <>
          <KpiGrid>
            {tile(t(`${L}kpi.domains`), lists.data.budget.domains)}
            {tile(t(`${L}kpi.regexps`), lists.data.budget.regexps)}
            {tile(t(`${L}kpi.cidrs`), lists.data.budget.cidrs)}
            <KpiTile label={t(`${L}kpi.problems`)} value={num.format(problems)} caption={t(`${L}kpi.problems_caption`)}
              color={problems > 0 ? md.error : undefined} pressed={stateFilter === 'problem'}
              onToggle={() => setFilter('lst_state', stateFilter === 'problem' ? null : 'problem')} />
          </KpiGrid>
          {overQuota && <Typography sx={{ fontSize: 13, color: md.error, mb: 1 }}>{t(`${L}kpi.over_quota`)}</Typography>}
          <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant, mb: 1 }}>
            {t(`${L}refresh_note`, { hours: lists.data.refresh_hours })}{' '}
            <Link component="button" type="button" underline="hover" sx={{ font: 'inherit', verticalAlign: 'baseline' }}
              onClick={() => onOpenSettings('refresh')}>{t(`${L}change`)}</Link>
          </Typography>
        </>
      )}
      {/* A fixed slot for the progress bar, so the table does not jump when a re-read starts. */}
      <Box sx={{ height: 4, mb: 0.5 }}>{lists.isFetching && !lists.isPending && <LinearProgress />}</Box>
      {lists.isRefetchError && lists.data && (
        <Typography sx={{ fontSize: 12.5, mb: 1, color: theme.palette.warning.dark }}>{t('admin:access_control.verdict.stale')}</Typography>
      )}
      {geosite.data && !geosite.data.current && (
        <Alert severity="info" sx={{ mb: 1.5 }} action={
          <AsyncButton size="small" color="inherit" onClick={async () => {
            try { await download.mutateAsync({}); pushSnack(t(`${L}snack.downloaded`), 'success') } catch (err) { reportDestError(t, err) }
          }}>{t(`${L}geosite_download`)}</AsyncButton>}>
          {t(`${L}geosite_missing`)}
        </Alert>
      )}
      {body}
      <ListDialog open={dialog.open} editId={dialog.editId} initialKind={dialog.kind} onClose={() => setDialog(d => ({ ...d, open: false }))}
        onSaved={onOpenEntries} />
      <Dialog open={inUse !== null} onClose={() => setInUse(null)} maxWidth="sm" fullWidth>
        <DialogTitle>{t('admin:access_control.confirm.list_in_use.title')}</DialogTitle>
        <DialogContent>
          <Typography sx={{ mb: 1 }}>{t('admin:access_control.confirm.list_in_use.message')}</Typography>
          <Box component="ul" sx={{ m: 0, pl: 2 }}>
            {(inUse?.usedBy ?? []).map(u => (
              <li key={`${u.kind}:${u.id}`}>
                <Link component="button" type="button" onClick={() => { setInUse(null); onOpenPolicy(u.id) }}>{u.name || String(u.id)}</Link>
              </li>
            ))}
          </Box>
        </DialogContent>
        <DialogActions><Button onClick={() => setInUse(null)}>{t('admin:access_control.confirm.list_in_use.action')}</Button></DialogActions>
      </Dialog>
    </Box>
  )
}
