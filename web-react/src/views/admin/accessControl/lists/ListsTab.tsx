import { useEffect, useRef, useState } from 'react'
import { Alert, Box, Button, IconButton, Menu, MenuItem, Paper, Skeleton, Stack, Table, TableBody, TableCell, TableHead, TableRow, Tooltip, Typography, useTheme } from '@mui/material'
import MoreVertIcon from '@mui/icons-material/MoreVert'
import { useLocation, useSearchParams } from 'react-router'
import { useAccessTranslation } from '@/views/admin/accessControl/useAccessTranslation'
import { useQueryClient } from '@tanstack/react-query'
import type { DestinationListSummary, DestinationPoliciesView, DestinationStatus } from '@/api/accessControl'
import KpiTile, { KpiGrid } from '@/components/KpiTile'
import { ToneBadge, stateTone } from '@/components/ToneBadge'
import { SortableTableCell } from '@/components/SortableTableCell'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'
import { useDestinationLists, useDeleteDestinationList, useRefreshDestinationList, useDestinationCategories, useRefreshDestinationCategories } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { accessControlKeys } from '@/query/keys'
import PendingActionGuard from '../PendingActionGuard'
import { destinationError } from '../errors'
import { listIsProblem, listSourceLabel } from './listDraft'
import ListDialog from './ListDialog'
import ListEntriesSheet from './ListEntriesSheet'
import UsedByPopover from './UsedByPopover'
import GeositeDownloadNotice from './GeositeDownloadNotice'
import { categoryRefreshState } from '@/utils/destinationCategories'
const P = 'admin:access_control.lists.'
export default function ListsTab({ active, selectedId, onCloseSheet, onOpenList, onOpenPolicy, onSettings, onTest, newListRequest = 0, policies, status }: { active: boolean; selectedId: number | null; onCloseSheet: () => void; onOpenList: (id: number) => void; onOpenPolicy: (id: number) => void; onSettings: () => void; onTest?: (target: string) => void; newListRequest?: number; policies?: DestinationPoliciesView; status?: DestinationStatus }) {
  const { t, dateTime, number } = useAccessTranslation(['admin', 'common']), scope = useQueryScope(), theme = useTheme()
  const query = useDestinationLists(scope, active || !!selectedId), remove = useDeleteDestinationList(scope), refresh = useRefreshDestinationList(scope)
  const categories = useDestinationCategories(scope, active), categoryRefresh = useRefreshDestinationCategories(scope)
  const catalogState = categoryRefreshState(categories.data, categories.error)
  const catalogMissing = !categories.data && destinationError(categories.error).status === 503
  const downloadCatalog = async () => { try { await categoryRefresh.mutateAsync() } catch { /* Keep definitions and show the download failure inline. */ } }
  const [params, setParams] = useSearchParams(), location = useLocation()
  const problem = params.get('lst_state') === 'problem'
  const [sort, setSort] = useState({ key: 'name', dir: 'asc' as 'asc' | 'desc' })
  const [editor, setEditor] = useState<{ existing?: DestinationListSummary } | null>(null)
  const lastNewListRequest = useRef(0)
  useEffect(() => { if (!active || newListRequest === lastNewListRequest.current) return; lastNewListRequest.current = newListRequest; setEditor({}) }, [active, newListRequest])
  const [afterClose, setAfterClose] = useState<number | 'settings' | null>(null)
  useEffect(() => { if (editor || afterClose === null) return; if (afterClose === 'settings') onSettings(); else onOpenList(afterClose); setAfterClose(null) }, [editor, afterClose, onOpenList, onSettings])
  const [menu, setMenu] = useState<{ anchor: HTMLElement; list: DestinationListSummary } | null>(null)
  const [busy, setBusy] = useState(false), admission = useRef(false)
  const enabled = new Set(policies ? [...policies.allow, ...policies.block, ...policies.observe].filter(row => row.enabled).map(row => row.id) : [])
  const items = query.data?.items ?? [], problemCount = items.filter(list => listIsProblem(list, enabled)).length
  const pendingUnknown = !policies && items.some(list => list.state === 'pending' && list.used_by.some(ref => ref.kind === 'policy'))
  const client = useQueryClient(), lastDetail = useRef<{ id: number; version: string } | null>(null)
  const selected = items.find(list => list.id === selectedId)
  const detailVersion = selected ? JSON.stringify([selected.updated_at, selected.state, selected.last_error, selected.last_fetched_at]) : ''
  useEffect(() => {
    if (!selectedId || !detailVersion) { lastDetail.current = null; return }
    if (lastDetail.current?.id === selectedId && lastDetail.current.version !== detailVersion) void client.invalidateQueries({ queryKey: [...accessControlKeys.listDetails(scope), selectedId] })
    lastDetail.current = { id: selectedId, version: detailVersion }
  }, [client, scope, selectedId, detailVersion])
  const rows = items.filter(list => !problem || listIsProblem(list, enabled)).slice().sort((a,b) => {
    const order = sort.key === 'name' ? a.name.localeCompare(b.name) : sort.key === 'count' ? a.entry_count - b.entry_count : a.updated_at - b.updated_at
    return (sort.dir === 'asc' ? order : -order) || a.id - b.id
  })
  const sortBy = (key: string, initial?: 'asc' | 'desc') => setSort(old => ({ key, dir: old.key === key ? old.dir === 'asc' ? 'desc' : 'asc' : initial ?? 'asc' }))
  const setProblem = () => setParams(prev => { const next = new URLSearchParams(prev); if (problem) next.delete('lst_state'); else next.set('lst_state', 'problem'); return next }, { replace: true, state: location.state })
  const run = async (work: () => Promise<void>) => {
    if (admission.current) return
    admission.current = true; setBusy(true)
    try { await work() } catch (err) { const details = destinationError(err); if (details.error === 'dest_list_in_use') { await confirm({ title: t(`${P}in_use_title`), message: t(`${P}in_use_message`), confirmText: t('common:actions.close') }) } else pushSnack(t(`${P}write_failed`, { error: details.error }), 'error') }
    finally { admission.current = false; setBusy(false) }
  }
  const refreshList = (list: DestinationListSummary) => void run(async () => { await refresh.mutateAsync(list.id); pushSnack(t(`${P}refresh_requested`), 'success') })
  const deleteList = (list: DestinationListSummary) => void run(async () => { if (!await confirm({ title: t(`${P}delete_title`, { name: list.name }), message: t(`${P}delete_message`), confirmText: t('common:actions.delete'), destructive: true })) return; await remove.mutateAsync(list.id); pushSnack(t(`${P}deleted`), 'success') })
  const state = (list: DestinationListSummary) => {
    const key = list.state === 'failed' ? list.last_fetched_at ? 'failed_old' : 'failed_first' : list.state === 'pending' && listIsProblem(list, enabled) ? 'pending_used' : list.state
    const tone = list.state === 'ready' ? 'ok' : list.state === 'failed' ? list.last_fetched_at ? 'attention' : 'failing' : key === 'pending_used' ? 'failing' : 'measuring'
    return <Stack spacing={.5}><Box><ToneBadge tone={stateTone(theme, tone)} label={t(`${P}state_${key}`)} /></Box>{!!list.parse_report_summary?.ignored_broad && <Typography variant="caption" color="warning.main">{t('admin:access_control.parse_report.broad_removed', { count: list.parse_report_summary.ignored_broad })}</Typography>}{list.last_error ? <Typography variant="caption" color="error" title={list.last_error} sx={{ overflowWrap: 'anywhere' }}>{list.last_error}</Typography> : list.last_fetched_at != null && <Typography variant="caption" color="text.secondary">{dateTime(list.last_fetched_at)}</Typography>}</Stack>
  }
  const usage = (list: DestinationListSummary) => <UsedByPopover name={list.name} references={list.used_by} ownerGroupId={list.owner_group_id} onOpenPolicy={onOpenPolicy} />
  const name = (list: DestinationListSummary) => <><Button sx={{ p: 0, justifyContent: 'flex-start', textAlign: 'left', overflowWrap: 'anywhere' }} onClick={() => onOpenList(list.id)}>{list.name}</Button><Typography variant="caption" color="text.secondary" sx={{ display: 'block', overflowWrap: 'anywhere' }}>{t(`${P}${list.kind}`)}{list.kind !== 'custom' && ` · ${listSourceLabel(list)}`}</Typography></>
  const actions = (list: DestinationListSummary) => <IconButton disabled={busy} aria-label={t(`${P}menu`, { name: list.name })} onClick={e => setMenu({ anchor: e.currentTarget, list })}><MoreVertIcon /></IconButton>
  return <>
    {busy && <PendingActionGuard />}
    <Box hidden={!active}>
      <Stack direction="row" sx={{ justifyContent: 'space-between', alignItems: 'center', gap: 1 }}><Typography component="h2" variant="h6">{t(`${P}title`)}</Typography><Button disabled={busy} onClick={() => setEditor({})}>{t(`${P}create`)}</Button></Stack>
      {catalogMissing && <GeositeDownloadNotice pending={categoryRefresh.isPending || catalogState.refreshing} disabled={busy} failed={!catalogState.refreshing && (!!categoryRefresh.error || catalogState.failed)} onDownload={downloadCatalog} />}
      {query.data ? <>
        {query.error && <Alert severity="warning" action={<Button onClick={() => void query.refetch()}>{t('common:actions.retry')}</Button>}>{t(`${P}stale`)}</Alert>}
        {pendingUnknown && <Alert severity="warning">{t(`${P}references_unknown`)}</Alert>}
        <KpiGrid>{(['domains', 'regexps', 'cidrs'] as const).map(kind => <KpiTile key={kind} label={t(`admin:access_control.quota.${kind}`)} value={query.data!.budget ? number(query.data!.budget[kind].used) : '—'} caption={t(`${P}limit`, { count: query.data!.budget?.[kind].limit ?? '—' })} />)}<KpiTile label={t(`${P}problem`)} value={pendingUnknown ? '—' : number(problemCount)} pressed={problem} onToggle={setProblem} /></KpiGrid>
        <Typography variant="body2" color="text.secondary" sx={{ mb: 2 }}>{t(`${P}refresh_interval`, { hours: query.data.refresh_hours })}<Button size="small" disabled={busy} onClick={onSettings}>{t('admin:access_control.list_editor.change_refresh')}</Button></Typography>
        {!items.length && <Alert severity="info">{t(`${P}empty`)}</Alert>}
        {!!items.length && <>
          <Table size="small" sx={{ display: { xs: 'none', sm: 'table' }, tableLayout: 'fixed' }}><TableHead><TableRow>{[{ key: 'name', label: 'name' }, { key: 'count', label: 'entries' }, { key: 'updated', label: 'status' }].map(col => <SortableTableCell key={col.key} column={col.key} activeColumn={sort.key} activeDir={sort.dir} onSort={sortBy} initialDir={col.key === 'updated' ? 'desc' : 'asc'}>{t(`${P}${col.label}`)}</SortableTableCell>)}<TableCell>{t(`${P}usage`)}</TableCell><TableCell sx={{ width: 50 }} /></TableRow></TableHead><TableBody>{rows.map(list => <TableRow key={list.id}><TableCell>{name(list)}</TableCell><TableCell>{list.entry_count ? number(list.entry_count) : '—'}</TableCell><TableCell>{state(list)}</TableCell><TableCell sx={{ overflowWrap: 'anywhere' }}>{usage(list)}</TableCell><TableCell>{actions(list)}</TableCell></TableRow>)}</TableBody></Table>
          <Stack spacing={1.5} sx={{ display: { xs: 'flex', sm: 'none' } }}>{rows.map(list => <Paper variant="outlined" key={list.id} sx={{ p: 2 }}><Stack direction="row" sx={{ justifyContent: 'space-between', gap: 1 }}><Box sx={{ minWidth: 0, flex: 1 }}>{name(list)}</Box>{actions(list)}</Stack><Box sx={{ my: 1 }}>{state(list)}</Box><Typography variant="body2">{t(`${P}entry_count`, { count: list.entry_count })}</Typography>{usage(list)}</Paper>)}</Stack>
        </>}
      </> : query.error ? <Alert severity="error" action={<Button onClick={() => void query.refetch()}>{t('common:actions.retry')}</Button>}>{t(`${P}failed`)}</Alert> : <Stack spacing={1} aria-busy="true">{[0,1,2,3,4].map(id => <Skeleton key={id} height={64} />)}</Stack>}
    </Box>
    <Menu anchorEl={menu?.anchor} open={!!menu} onClose={() => setMenu(null)}>{menu && [
      <MenuItem key="edit" onClick={() => { setEditor({ existing: menu.list }); setMenu(null) }}>{t('common:actions.edit')}</MenuItem>,
      ...(menu.list.kind === 'custom' ? [] : [<MenuItem key="refresh" disabled={menu.list.state === 'refreshing'} onClick={() => { refreshList(menu.list); setMenu(null) }}>{t(`${P}refresh_now`)}</MenuItem>]),
      <Tooltip key="delete" title={menu.list.used_by.length || menu.list.owner_group_id ? t(`${P}in_use_message`) : ''}><span><MenuItem disabled={!!menu.list.used_by.length || !!menu.list.owner_group_id} onClick={() => { deleteList(menu.list); setMenu(null) }}>{t('common:actions.delete')}</MenuItem></span></Tooltip>,
    ]}</Menu>
    {selectedId && <ListEntriesSheet key={`sheet-${selectedId}`} id={selectedId} onTest={onTest} refreshing={selected?.state === 'refreshing'} onClose={onCloseSheet} busy={busy} onEdit={() => { const existing = items.find(list => list.id === selectedId); if (existing) setEditor({ existing }) }} onRefresh={() => { const list = items.find(list => list.id === selectedId); if (list) refreshList(list) }} />}
    {editor && <ListDialog key={`editor-${editor.existing?.id ?? 'new'}`} existing={editor.existing} policies={policies} status={status} refreshHours={query.data?.refresh_hours ?? 24} onClose={() => setEditor(null)} onSaved={list => { setAfterClose(list.id); setEditor(null) }} onSettings={() => { setAfterClose('settings'); setEditor(null) }} />}
  </>
}
