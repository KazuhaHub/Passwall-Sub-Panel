import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  Accordion, AccordionDetails, AccordionSummary, Alert, Box, Button, Card, Checkbox, CircularProgress, Dialog, DialogActions, DialogContent,
  DialogTitle, FormControl, FormControlLabel, IconButton, InputAdornment, InputLabel, LinearProgress,
  MenuItem, Select, Tab, Tabs, Table, TableBody, TableCell, TableContainer, TableHead, TableRow,
  TextField, Typography, useMediaQuery, useTheme,
} from '@mui/material'
import DoneIcon from '@mui/icons-material/Done'
import RefreshIcon from '@mui/icons-material/Refresh'
import SearchIcon from '@mui/icons-material/Search'
import ClearIcon from '@mui/icons-material/Close'
import VisibilityIcon from '@mui/icons-material/VisibilityOutlined'
import ExpandMoreIcon from '@mui/icons-material/ExpandMore'
import { useTranslation } from 'react-i18next'

import { acknowledgeNodeIssue, listNodeIssues, type NodeIssueListParams } from '@/api/nodeIssues'
import type { NodeAgentIssue } from '@/api/types'
import PageHeader from '@/components/PageHeader'
import { confirm } from '@/components/ConfirmHost'
import { PagedTableFooter } from '@/components/PagedTableFooter'
import { pushSnack } from '@/components/SnackbarHost'
import { useSiteStore } from '@/stores/site'
import { formatDualTz } from '@/utils/datetime'
import { groupNodeIssues, type NodeIssueGroup } from '@/utils/nodeIssueGroups'
import { useCan } from '@/utils/permissions'
import { allSettledLimited } from '@/utils/promises'

type ReviewFilter = '' | 'false' | 'true'
type IssueView = 'attention' | 'diagnostic' | 'all'

function initialPageSize(): number {
  try {
    const raw = localStorage.getItem('psp_page_size')
    const n = raw ? parseInt(raw, 10) : 25
    return Number.isFinite(n) && n > 0 ? n : 25
  } catch { return 25 }
}

function compactTime(value: string, timezone: string, language: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  const options: Intl.DateTimeFormatOptions = {
    year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit',
  }
  try { return date.toLocaleString(language, { ...options, timeZone: timezone || undefined }) }
  catch { return date.toLocaleString(undefined, options) }
}

export default function NodeIssuesView() {
  const theme = useTheme()
  const md = theme.palette.md
  const narrow = useMediaQuery(theme.breakpoints.down('lg'))
  const { t, i18n } = useTranslation(['admin', 'common'])
  const canOperate = useCan('sync.operate')
  const panelTz = useSiteStore(s => s.timezone)
  const [items, setItems] = useState<NodeAgentIssue[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(initialPageSize)
  const [review, setReview] = useState<ReviewFilter>('false')
  const [issueView, setIssueView] = useState<IssueView>('attention')
  const [search, setSearch] = useState('')
  const [keyword, setKeyword] = useState('')
  const [loading, setLoading] = useState(false)
  const [loadFailed, setLoadFailed] = useState(false)
  const [busyID, setBusyID] = useState<number | null>(null)
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [batchState, setBatchState] = useState<'' | 'confirming' | 'running'>('')
  const [batchProgress, setBatchProgress] = useState(0)
  const [batchTotal, setBatchTotal] = useState(0)
  const [activeGroupID, setActiveGroupID] = useState<string | null>(null)
  const loadController = useRef<AbortController | null>(null)
  const actionLock = useRef(false)
  const latestCanOperate = useRef(canOperate)
  latestCanOperate.current = canOperate
  const groups = useMemo(() => groupNodeIssues(items), [items])
  const activeGroup = groups.find(group => group.id === activeGroupID)
  const pendingItems = items.filter(issue => !issue.acknowledged_at)
  const selectedIDs = pendingItems.filter(issue => selected.has(issue.id)).map(issue => issue.id)
  const actionBusy = busyID !== null || batchState !== ''

  const load = useCallback(async (retainIDs?: ReadonlySet<number>) => {
    loadController.current?.abort()
    const controller = new AbortController()
    loadController.current = controller
    setLoading(true)
    setLoadFailed(false)
    setSelected(previous => retainIDs ? new Set([...previous].filter(id => retainIDs.has(id))) : new Set())
    try {
      const params: NodeIssueListParams = { page, page_size: pageSize, view: issueView }
      if (review !== '') params.acknowledged = review === 'true'
      if (keyword) params.keyword = keyword
      const response = await listNodeIssues(params, controller.signal)
      if (controller.signal.aborted) return
      const lastPage = Math.max(1, Math.ceil(response.total / pageSize))
      if (page > lastPage) { setPage(lastPage); return }
      setItems(response.items)
      setTotal(response.total)
      setSelected(new Set(response.items.filter(issue => !issue.acknowledged_at && retainIDs?.has(issue.id)).map(issue => issue.id)))
    } catch {
      if (!controller.signal.aborted) setLoadFailed(true)
    } finally {
      if (!controller.signal.aborted) setLoading(false)
    }
  }, [page, pageSize, review, keyword, issueView])
  const latestLoad = useRef(load)
  useEffect(() => {
    latestLoad.current = load
    setActiveGroupID(null)
    void load()
    return () => loadController.current?.abort()
  }, [load])
  useEffect(() => {
    if (!loading && activeGroupID && !activeGroup) setActiveGroupID(null)
  }, [loading, activeGroupID, activeGroup])
  useEffect(() => { if (!canOperate) setSelected(new Set()) }, [canOperate])

  function setPageSizePersist(value: number) {
    if (actionLock.current) return
    setPageSize(value)
    setPage(1)
    try { localStorage.setItem('psp_page_size', String(value)) } catch { /* ignore */ }
  }

  function submitSearch() {
    if (actionLock.current) return
    const next = search.trim()
    if (page === 1 && next === keyword) void load()
    else { setPage(1); setKeyword(next) }
  }

  async function acknowledge(issue: NodeAgentIssue) {
    if (!canOperate || issue.acknowledged_at || actionLock.current) return
    actionLock.current = true
    setBusyID(issue.id)
    try {
      await acknowledgeNodeIssue(issue.id)
      pushSnack(t('admin:node_issues.acknowledged'), 'success')
      await latestLoad.current()
    } catch { /* Shared API toast reports failure; never mark reviewed on failure. */ }
    finally { actionLock.current = false; setBusyID(null) }
  }

  function toggleSelection(ids: readonly number[], checked: boolean) {
    if (!canOperate || actionLock.current || loading || loadFailed) return
    setSelected(previous => {
      const next = new Set(previous)
      ids.forEach(id => checked ? next.add(id) : next.delete(id))
      return next
    })
  }

  async function acknowledgeSelected() {
    if (!canOperate || !selectedIDs.length || actionLock.current || loading || loadFailed) return
    // Capture exact original IDs before confirmation. Never expand the action
    // to another page, a fresh filter result or newly-arrived records.
    const ids = [...new Set(selectedIDs)]
    actionLock.current = true
    setBatchState('confirming')
    setBatchProgress(0)
    setBatchTotal(ids.length)
    try {
      const agreed = await confirm({
        title: t('admin:node_issues.bulk.confirm_title'),
        message: t('admin:node_issues.bulk.confirm_message', {
          records: t('admin:node_issues.record_count', { count: ids.length }),
        }),
        confirmText: t('admin:node_issues.bulk.acknowledge'),
      })
      if (!agreed || !latestCanOperate.current) return
      setBatchState('running')
      const results = await allSettledLimited(ids, async id => {
        try { await acknowledgeNodeIssue(id, { quiet: true }) }
        finally { setBatchProgress(value => value + 1) }
      })
      const failed = new Set(ids.filter((_, index) => results[index].status === 'rejected'))
      setSelected(failed)
      pushSnack(t(failed.size ? 'admin:node_issues.bulk.partial' : 'admin:node_issues.bulk.done', {
        ok: ids.length - failed.size, fail: failed.size,
        records: t('admin:node_issues.record_count', { count: ids.length }),
      }), failed.size ? 'warning' : 'success')
      // Preserve only failures that still appear unreviewed in the refreshed
      // result. Successful requests do not fabricate local review timestamps.
      await latestLoad.current(failed)
    } finally {
      actionLock.current = false
      setBatchState('')
    }
  }

  function batchControls() {
    return canOperate && selectedIDs.length > 0 && <Box sx={{
      display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: 1.5,
    }} aria-busy={batchState === 'running'}>
      <Typography variant="body2">{t('admin:node_issues.bulk.selected', {
        records: t('admin:node_issues.record_count', { count: selectedIDs.length }),
      })}</Typography>
      <Button size="small" disabled={actionBusy || loading} onClick={() => setSelected(new Set())}>
        {t('admin:node_issues.bulk.clear')}
      </Button>
      <Button size="small" variant="contained" disabled={actionBusy || loading || loadFailed}
        startIcon={batchState === 'running' ? <CircularProgress size={14} /> : <DoneIcon />}
        onClick={() => void acknowledgeSelected()}>{t('admin:node_issues.bulk.acknowledge')}</Button>
      {batchState === 'running' && <Typography variant="caption" role="status">
        {t('admin:node_issues.bulk.progress', { done: batchProgress, total: batchTotal })}
      </Typography>}
    </Box>
  }

  function groupSelection(group: NodeIssueGroup) {
    if (!canOperate) return null
    const ids = group.issues.filter(issue => !issue.acknowledged_at).map(issue => issue.id)
    const all = ids.length > 0 && ids.every(id => selected.has(id))
    const some = ids.some(id => selected.has(id))
    return <Checkbox size="small" checked={all} indeterminate={some && !all}
      disabled={!ids.length || actionBusy || loading || loadFailed}
      slotProps={{ input: { 'aria-label': t('admin:node_issues.bulk.select_group', {
        problem: issueTitle(group), server: serverName(group),
      }) } }} onChange={(_, checked) => toggleSelection(ids, checked)} />
  }

  function issueTitle(group: NodeIssueGroup) {
    if (issueView === 'diagnostic' && group.category === 'statistics') return t('admin:node_issues.startup_title')
    return t(`admin:node_issues.category_titles.${group.category}`)
  }

  function suggestedAction(group: NodeIssueGroup) {
    return t(issueView === 'diagnostic' && group.category === 'statistics'
      ? 'admin:node_issues.startup_action' : `admin:node_issues.category_actions.${group.category}`)
  }

  function serverName(group: NodeIssueGroup) {
    return group.issues.find(record => record.server_name)?.server_name || t('admin:node_issues.unknown_server')
  }

  function reviewState(group: NodeIssueGroup) {
    const key = group.unacknowledgedCount === 0 ? 'acknowledged'
      : group.unacknowledgedCount === group.issues.length ? 'unacknowledged' : 'mixed'
    return <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mt: 0.5 }}>
      {t(`admin:node_issues.state.${key}`)}
    </Typography>
  }

  function problemCell(group: NodeIssueGroup) {
    return <Box sx={{ minWidth: 0 }}>
      <Typography sx={{ fontSize: 14, fontWeight: 500, overflowWrap: 'anywhere' }}>{issueTitle(group)}</Typography>
      <Typography variant="body2" color="text.secondary" sx={{ mt: 0.5, overflowWrap: 'anywhere' }}>
        {suggestedAction(group)}
      </Typography>
      {review !== 'false' && reviewState(group)}
    </Box>
  }

  function serverCell(group: NodeIssueGroup) {
    const issue = group.issues.find(record => record.server_name)
    return <Box sx={{ minWidth: 0 }}>
      <Typography sx={{ fontSize: 13, overflowWrap: 'anywhere' }}>{serverName(group)}</Typography>
      <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mt: 0.5 }}>
        {issue?.server_id ? t('admin:node_issues.server_id', { id: issue.server_id })
          : t('admin:node_issues.unlinked_hint')}
      </Typography>
    </Box>
  }

  function detailButton(group: NodeIssueGroup) {
    return <Button size="small" startIcon={<VisibilityIcon />} sx={{ whiteSpace: 'nowrap' }}
      aria-label={t('admin:node_issues.open_details', { problem: issueTitle(group), server: serverName(group) })}
      onClick={() => setActiveGroupID(group.id)}>{t('admin:node_issues.view_details')}</Button>
  }

  function detailField(label: string, value: string, mono = false) {
    return <Box>
      <Typography variant="caption" color="text.secondary">{label}</Typography>
      <Typography sx={{ mt: 0.25, fontSize: 13, whiteSpace: 'pre-wrap', overflowWrap: 'anywhere',
        ...(mono ? { fontFamily: 'monospace', userSelect: 'text' } : {}) }}>{value || '—'}</Typography>
    </Box>
  }

  return (
    <Box sx={{ p: { xs: 2, sm: 3 }, minWidth: 0 }}>
      <PageHeader title={t('admin:node_issues.title')} subtitle={t('admin:node_issues.subtitle')}
        actions={<Button variant="outlined" startIcon={<RefreshIcon />} disabled={loading || actionBusy}
          onClick={() => void load()}>{t('admin:node_issues.refresh')}</Button>} />
      <Tabs value={issueView} variant="fullWidth" aria-label={t('admin:node_issues.view.label')}
        sx={{ mb: 2, maxWidth: 520 }}
        onChange={(_, value: IssueView) => { if (!actionLock.current) { setIssueView(value); setPage(1) } }}>
        {(['attention', 'diagnostic', 'all'] as const).map(value => <Tab key={value} value={value}
          id={`node-issues-tab-${value}`} aria-controls="node-issues-panel"
          disabled={actionBusy} label={t(`admin:node_issues.view.${value}`)} sx={{ minWidth: 0 }} />)}
      </Tabs>
      <Box component="form" onSubmit={event => { event.preventDefault(); submitSearch() }}
        sx={{ display: 'flex', gap: 1.5, mb: 2, flexWrap: 'wrap' }}>
        <FormControl size="small" sx={{ minWidth: 170, flex: { xs: '1 1 100%', sm: '0 0 auto' } }}>
          <InputLabel id="issue-review-label" shrink>{t('admin:node_issues.table.state')}</InputLabel>
          <Select labelId="issue-review-label" label={t('admin:node_issues.table.state')} value={review} displayEmpty disabled={actionBusy}
            onChange={event => { if (!actionLock.current) { setReview(event.target.value as ReviewFilter); setPage(1) } }}>
            <MenuItem value="">{t('admin:node_issues.filter.all')}</MenuItem>
            <MenuItem value="false">{t('admin:node_issues.filter.unacknowledged')}</MenuItem>
            <MenuItem value="true">{t('admin:node_issues.filter.acknowledged')}</MenuItem>
          </Select>
        </FormControl>
        <TextField size="small" value={search} placeholder={t('admin:node_issues.search')} disabled={actionBusy}
          onChange={event => setSearch(event.target.value)} sx={{ flex: '1 1 240px', maxWidth: { sm: 460 } }}
          slotProps={{ htmlInput: { 'aria-label': t('admin:node_issues.search') }, input: {
            endAdornment: <InputAdornment position="end">
              {search && <IconButton size="small" disabled={actionBusy} aria-label={t('admin:node_issues.clear_search')}
                onClick={() => { setSearch(''); setKeyword(''); setPage(1) }}><ClearIcon fontSize="small" /></IconButton>}
              <IconButton type="submit" size="small" disabled={actionBusy} aria-label={t('admin:node_issues.search_action')}><SearchIcon fontSize="small" /></IconButton>
            </InputAdornment>,
          } }} />
      </Box>
      {loadFailed && <Alert severity="error" sx={{ mb: 2 }}>{t('admin:node_issues.load_failed')}</Alert>}
      <Card sx={{ bgcolor: md.surfaceContainerLow, overflow: 'hidden' }} aria-busy={loading}
        role="tabpanel" id="node-issues-panel" aria-labelledby={`node-issues-tab-${issueView}`}>
        <Box sx={{ px: 2.5, py: 1.5, borderBottom: `1px solid ${md.outlineVariant}` }}>
          <Typography variant="body2">{t('admin:node_issues.page_summary', {
            records: t('admin:node_issues.record_count', { count: items.length }),
            groups: t('admin:node_issues.group_count', { count: groups.length }),
          })}</Typography>
          <Typography variant="caption" color="text.secondary">{t('admin:node_issues.review_notice')}</Typography>
          {canOperate && pendingItems.length > 0 && <FormControlLabel sx={{ display: 'flex', width: 'fit-content', mt: 0.5, mb: -0.5 }}
            control={<Checkbox size="small" checked={selectedIDs.length === pendingItems.length}
              indeterminate={selectedIDs.length > 0 && selectedIDs.length < pendingItems.length}
              disabled={actionBusy || loading || loadFailed}
              slotProps={{ input: { 'aria-label': t('admin:node_issues.bulk.select_page') } }}
              onChange={(_, checked) => toggleSelection(pendingItems.map(issue => issue.id), checked)} />}
            label={<Typography variant="caption">{t('admin:node_issues.bulk.select_page')}</Typography>} />}
          {selectedIDs.length > 0 && <Box sx={{ mt: 1 }}>{batchControls()}</Box>}
        </Box>
        {loading && <LinearProgress aria-label={t('admin:node_issues.loading')} />}
        {!loading && items.length === 0 && !loadFailed && <Typography sx={{ py: 6, px: 3, textAlign: 'center', color: md.onSurfaceVariant }}>
          {t(review === 'false' && !keyword ? (issueView === 'attention'
            ? 'admin:node_issues.empty_attention' : 'admin:node_issues.empty_review') : 'admin:node_issues.empty')}
        </Typography>}
        {narrow ? <Box>
          {groups.map(group => <Box key={group.id} sx={{ p: 2.5, borderBottom: `1px solid ${md.outlineVariant}` }}>
            <Box sx={{ display: 'flex', alignItems: 'flex-start', gap: 1 }}>
              {groupSelection(group)}<Box sx={{ flex: 1, minWidth: 0 }}>{problemCell(group)}</Box>
            </Box>
            <Box sx={{ mt: 2 }}>{serverCell(group)}</Box>
            <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mt: 1.5 }}>
              {t('admin:node_issues.mobile_meta', { records: t('admin:node_issues.record_count', { count: group.issues.length }),
                time: compactTime(group.lastSeenAt, panelTz, i18n.language), timezone: panelTz })}
            </Typography>
            <Box sx={{ mt: 1 }}>{detailButton(group)}</Box>
          </Box>)}
        </Box> : groups.length > 0 && <TableContainer>
          <Table size="small" sx={{ tableLayout: 'fixed' }}>
            <TableHead><TableRow sx={{ '& th': { color: md.onSurfaceVariant, fontWeight: 500, fontSize: 12 } }}>
              {canOperate && <TableCell padding="checkbox" sx={{ width: 48 }} />}
              <TableCell sx={{ width: '34%' }}>{t('admin:node_issues.table.problem')}</TableCell>
              <TableCell sx={{ width: '24%' }}>{t('admin:node_issues.table.server')}</TableCell>
              <TableCell sx={{ width: 76 }}>{t('admin:node_issues.table.records')}</TableCell>
              <TableCell sx={{ width: 170 }}>{t('admin:node_issues.table.last_seen')}</TableCell>
              <TableCell sx={{ width: 140 }} align="right">{t('admin:node_issues.table.actions')}</TableCell>
            </TableRow></TableHead>
            <TableBody>{groups.map(group => <TableRow key={group.id} hover sx={{
              '& td': { py: 2.5, verticalAlign: 'top', borderBottom: `1px solid ${md.outlineVariant}` },
            }}>
              {canOperate && <TableCell padding="checkbox">{groupSelection(group)}</TableCell>}
              <TableCell>{problemCell(group)}</TableCell>
              <TableCell>{serverCell(group)}</TableCell>
              <TableCell sx={{ fontSize: 13 }}>{group.issues.length}</TableCell>
              <TableCell sx={{ fontSize: 12, overflowWrap: 'anywhere' }} title={formatDualTz(group.lastSeenAt, panelTz)}>
                {compactTime(group.lastSeenAt, panelTz, i18n.language)}
                {panelTz && <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mt: 0.5 }}>{panelTz}</Typography>}
              </TableCell>
              <TableCell align="right">{detailButton(group)}</TableCell>
            </TableRow>)}</TableBody>
          </Table>
        </TableContainer>}
        <PagedTableFooter total={total} page={page} pageSize={pageSize}
          disabled={actionBusy} onPageChange={value => { if (!actionLock.current) setPage(value) }} onPageSizeChange={setPageSizePersist} />
      </Card>
      <Dialog open={Boolean(activeGroup)} onClose={() => setActiveGroupID(null)} fullWidth maxWidth="md"
        aria-labelledby="node-issue-details-title">
        <DialogTitle id="node-issue-details-title">{activeGroup && issueTitle(activeGroup)}</DialogTitle>
        <DialogContent dividers>
          {activeGroup && <>
            <Typography sx={{ mb: 2, fontWeight: 500 }}>{serverName(activeGroup)}</Typography>
            <Typography variant="body2" color="text.secondary" sx={{ mb: 2 }}>
              {suggestedAction(activeGroup)}
            </Typography>
            <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mb: 2 }}>{t('admin:node_issues.detail_scope', {
              records: t('admin:node_issues.record_count', { count: activeGroup.issues.length }),
            })}</Typography>
            {activeGroup.issues.map(issue => <Box key={issue.id} sx={{
              py: 1.25, borderBottom: `1px solid ${md.outlineVariant}`,
            }}>
              <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 1, flexWrap: 'wrap' }}>
                <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                  {!issue.acknowledged_at && canOperate && <Checkbox size="small" checked={selected.has(issue.id)}
                    disabled={actionBusy || loading || loadFailed}
                    slotProps={{ input: { 'aria-label': t('admin:node_issues.bulk.select_record', { id: issue.id }) } }}
                    onChange={(_, checked) => toggleSelection([issue.id], checked)} />}
                  <Typography sx={{ fontSize: 13, fontWeight: 500 }}>{t('admin:node_issues.record_id', { id: issue.id })}</Typography>
                </Box>
                <Typography variant="caption" color="text.secondary">
                  {t(`admin:node_issues.state.${issue.acknowledged_at ? 'acknowledged' : 'unacknowledged'}`)}
                </Typography>
                {!issue.acknowledged_at && canOperate && <Button size="small"
                  startIcon={busyID === issue.id ? <CircularProgress size={14} /> : <DoneIcon />}
                  disabled={actionBusy || loading} onClick={() => void acknowledge(issue)}>
                  {t('admin:node_issues.acknowledge')}
                </Button>}
              </Box>
              <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mt: 0.5 }}>
                {issueView === 'diagnostic' ? t('admin:node_issues.startup_title')
                  : t(`admin:node_issues.code_titles.${issue.code}`, { defaultValue: t('admin:node_issues.unknown_title') })}
              </Typography>
            </Box>)}
            <Accordion key={activeGroup.id} disableGutters elevation={0} sx={{ mt: 2, bgcolor: 'transparent' }}>
              <AccordionSummary expandIcon={<ExpandMoreIcon />}>
                <Typography variant="body2">{t('admin:node_issues.technical_details')}</Typography>
              </AccordionSummary>
              <AccordionDetails sx={{ px: 0 }}>
                <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', sm: '1fr 1fr' }, gap: 2, mb: 2 }}>
                  {detailField(t('admin:node_issues.table.code'), [...new Set(activeGroup.issues.map(issue => issue.code))].join('\n'), true)}
                  {detailField(t('admin:node_issues.table.agent'), activeGroup.agentID, true)}
                </Box>
                {activeGroup.issues.map(issue => <Box key={issue.id} sx={{ p: 2, mb: 1.5,
                  border: `1px solid ${md.outlineVariant}`, borderRadius: 1.5 }}>
                  <Typography variant="caption" color="text.secondary">#{issue.id}</Typography>
                  {activeGroup.issues.some(record => record.code !== activeGroup.code)
                    && detailField(t('admin:node_issues.table.code'), issue.code, true)}
                  {detailField(t('admin:node_issues.table.key'), issue.key || '—', true)}
                  <Box sx={{ mt: 1.5 }}>{detailField(t('admin:node_issues.table.detail'), issue.detail || '—')}</Box>
                  <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', sm: '1fr 1fr' }, gap: 2, mt: 2 }}>
                    {detailField(t('admin:node_issues.table.first_seen'), formatDualTz(issue.first_seen_at, panelTz))}
                    {detailField(t('admin:node_issues.table.last_seen'), formatDualTz(issue.last_seen_at, panelTz))}
                    {issue.acknowledged_at && detailField(t('admin:node_issues.reviewed_at'), formatDualTz(issue.acknowledged_at, panelTz))}
                  </Box>
                </Box>)}
              </AccordionDetails>
            </Accordion>
          </>}
        </DialogContent>
        <DialogActions sx={{ flexWrap: 'wrap', gap: 1 }}>{batchControls()}
          <Button onClick={() => setActiveGroupID(null)}>{t('common:actions.close')}</Button>
        </DialogActions>
      </Dialog>
    </Box>
  )
}
