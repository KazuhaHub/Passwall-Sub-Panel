import { useEffect, useState } from 'react'
import {
  Box,
  Button,
  Card,
  CircularProgress,
  MenuItem,
  Select,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  TextField,
  Tooltip,
  useTheme,
} from '@mui/material'
import DoneIcon from '@mui/icons-material/Done'
import RefreshIcon from '@mui/icons-material/Refresh'
import { useTranslation } from 'react-i18next'

import { acknowledgeNodeIssue, listNodeIssues, type NodeIssueListParams } from '@/api/nodeIssues'
import type { NodeAgentIssue } from '@/api/types'
import PageHeader from '@/components/PageHeader'
import { PagedTableFooter } from '@/components/PagedTableFooter'
import { pushSnack } from '@/components/SnackbarHost'
import { useSiteStore } from '@/stores/site'
import { formatDualTz } from '@/utils/datetime'
import { useCan } from '@/utils/permissions'

type ReviewFilter = '' | 'false' | 'true'

function initialPageSize(): number {
  try {
    const raw = localStorage.getItem('psp_page_size')
    const n = raw ? parseInt(raw, 10) : 25
    return Number.isFinite(n) && n > 0 ? n : 25
  } catch { return 25 }
}

export default function NodeIssuesView() {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation(['admin', 'common'])
  const canOperate = useCan('sync.operate')
  const panelTz = useSiteStore(s => s.timezone)
  const [items, setItems] = useState<NodeAgentIssue[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(initialPageSize)
  const [review, setReview] = useState<ReviewFilter>('false')
  const [search, setSearch] = useState('')
  const [keyword, setKeyword] = useState('')
  const [loading, setLoading] = useState(false)
  const [busyID, setBusyID] = useState<number | null>(null)

  useEffect(() => { void load() }, [page, pageSize, review, keyword]) // eslint-disable-line react-hooks/exhaustive-deps

  async function load() {
    setLoading(true)
    try {
      const params: NodeIssueListParams = { page, page_size: pageSize }
      if (review !== '') params.acknowledged = review === 'true'
      if (keyword) params.keyword = keyword
      const response = await listNodeIssues(params)
      setItems(response.items)
      setTotal(response.total)
    } finally {
      setLoading(false)
    }
  }

  function setPageSizePersist(value: number) {
    setPageSize(value)
    setPage(1)
    try { localStorage.setItem('psp_page_size', String(value)) } catch { /* ignore */ }
  }

  function submitSearch() {
    setPage(1)
    setKeyword(search.trim())
  }

  async function acknowledge(issue: NodeAgentIssue) {
    setBusyID(issue.id)
    try {
      await acknowledgeNodeIssue(issue.id)
      pushSnack(t('admin:node_issues.acknowledged'), 'success')
      await load()
    } finally {
      setBusyID(null)
    }
  }

  return (
    <Box sx={{ p: 3 }}>
      <PageHeader
        title={t('admin:node_issues.title')}
        subtitle={t('admin:node_issues.subtitle')}
        actions={<Button variant="contained" startIcon={<RefreshIcon />} onClick={() => load()}>{t('admin:node_issues.refresh')}</Button>}
      />
      <Box sx={{ display: 'flex', gap: 1.5, mb: 2, flexWrap: 'wrap' }}>
        <Select
          size="small" value={review} displayEmpty sx={{ minWidth: 180 }}
          onChange={event => { setReview(event.target.value as ReviewFilter); setPage(1) }}
        >
          <MenuItem value="">{t('admin:node_issues.filter.all')}</MenuItem>
          <MenuItem value="false">{t('admin:node_issues.filter.unacknowledged')}</MenuItem>
          <MenuItem value="true">{t('admin:node_issues.filter.acknowledged')}</MenuItem>
        </Select>
        <TextField
          size="small" value={search} placeholder={t('admin:node_issues.search')}
          onChange={event => setSearch(event.target.value)}
          onKeyDown={event => { if (event.key === 'Enter') submitSearch() }}
          sx={{ minWidth: 280 }}
        />
        <Button variant="outlined" onClick={submitSearch}>{t('admin:node_issues.search_action')}</Button>
      </Box>
      <Card sx={{ bgcolor: md.surfaceContainerLow, overflow: 'hidden' }}>
        <TableContainer>
          <Table>
            <TableHead>
              <TableRow sx={{ '& th': { color: md.onSurfaceVariant, fontWeight: 500, fontSize: 12, whiteSpace: 'nowrap' } }}>
                <TableCell>{t('admin:node_issues.table.state')}</TableCell>
                <TableCell>{t('admin:node_issues.table.code')}</TableCell>
                <TableCell>{t('admin:node_issues.table.agent')}</TableCell>
                <TableCell>{t('admin:node_issues.table.key')}</TableCell>
                <TableCell>{t('admin:node_issues.table.detail')}</TableCell>
                <TableCell>{t('admin:node_issues.table.first_seen')}</TableCell>
                <TableCell>{t('admin:node_issues.table.last_seen')}</TableCell>
                <TableCell align="right">{t('admin:node_issues.table.actions')}</TableCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {loading && items.length === 0 && (
                <TableRow><TableCell colSpan={8} align="center" sx={{ py: 6 }}><CircularProgress size={24} /></TableCell></TableRow>
              )}
              {!loading && items.length === 0 && (
                <TableRow><TableCell colSpan={8} align="center" sx={{ py: 6, color: md.onSurfaceVariant }}>{t('admin:node_issues.empty')}</TableCell></TableRow>
              )}
              {items.map(issue => {
                const acknowledged = Boolean(issue.acknowledged_at)
                return (
                  <TableRow key={issue.id} hover sx={{ '& td': { borderBottom: `1px solid ${md.outlineVariant}` } }}>
                    <TableCell>
                      <Box component="span" sx={{
                        display: 'inline-block', px: 1.25, py: 0.25, borderRadius: 1, fontSize: 12,
                        bgcolor: acknowledged ? md.surfaceContainerHighest : md.errorContainer,
                        color: acknowledged ? md.onSurfaceVariant : md.onErrorContainer,
                      }}>
                        {t(`admin:node_issues.state.${acknowledged ? 'acknowledged' : 'unacknowledged'}`)}
                      </Box>
                    </TableCell>
                    <TableCell sx={{ fontFamily: 'monospace', fontSize: 12, whiteSpace: 'nowrap' }}>{issue.code}</TableCell>
                    <TableCell sx={{ fontFamily: 'monospace', fontSize: 12, whiteSpace: 'nowrap' }}>{issue.agent_id}</TableCell>
                    <TableCell sx={{ fontFamily: 'monospace', fontSize: 12, whiteSpace: 'nowrap' }}>{issue.key || '—'}</TableCell>
                    <TableCell sx={{ maxWidth: 380, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontSize: 13 }}>
                      <Tooltip title={issue.detail || ''}><span>{issue.detail || '—'}</span></Tooltip>
                    </TableCell>
                    <TableCell sx={{ whiteSpace: 'nowrap', fontSize: 13 }}>{formatDualTz(issue.first_seen_at, panelTz)}</TableCell>
                    <TableCell sx={{ whiteSpace: 'nowrap', fontSize: 13 }}>{formatDualTz(issue.last_seen_at, panelTz)}</TableCell>
                    <TableCell align="right">
                      {!acknowledged && canOperate && (
                        <Button
                          size="small" startIcon={busyID === issue.id ? <CircularProgress size={14} /> : <DoneIcon />}
                          disabled={busyID !== null} onClick={() => acknowledge(issue)}
                        >
                          {t('admin:node_issues.acknowledge')}
                        </Button>
                      )}
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        </TableContainer>
        <PagedTableFooter
          total={total} page={page} pageSize={pageSize}
          onPageChange={setPage} onPageSizeChange={setPageSizePersist}
        />
      </Card>
    </Box>
  )
}
