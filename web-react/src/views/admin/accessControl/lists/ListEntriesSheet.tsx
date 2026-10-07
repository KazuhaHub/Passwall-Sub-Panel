import { useState } from 'react'
import { Alert, Box, Button, Chip, Dialog, DialogContent, DialogTitle, IconButton, Skeleton, Stack, TextField, Typography } from '@mui/material'
import CloseIcon from '@mui/icons-material/Close'
import { useTranslation } from 'react-i18next'
import { useDestinationList } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import ParseReport from './ParseReport'
const P = 'admin:access_control.list_entries.'
function entryKind(entry: string): string { const kind = entry.split(':', 1)[0]; return ['domain', 'full', 'keyword', 'regexp'].includes(kind) ? kind : 'cidr' }
export default function ListEntriesSheet({ id, onClose, onEdit, onRefresh, onTest, busy, refreshing = false }: { id: number; onClose: () => void; onEdit: () => void; onRefresh: () => void; onTest?: (target: string) => void; busy: boolean; refreshing?: boolean }) {
  const { t } = useTranslation(['admin', 'common']), query = useDestinationList(useQueryScope(), id)
  const [search, setSearch] = useState(''), [type, setType] = useState<string | null>(null)
  const entries = query.data?.entries ?? [], counts = query.data?.entry_types ?? Object.fromEntries(['domain', 'full', 'keyword', 'regexp', 'cidr'].map(kind => [kind, entries.filter(entry => entryKind(entry) === kind).length]))
  const filtered = entries.filter(entry => (!type || entryKind(entry) === type) && entry.toLowerCase().includes(search.toLowerCase()))
  return <Dialog open onClose={busy ? undefined : onClose} maxWidth={false} aria-labelledby="access-list-entries-title" sx={{ '& .MuiDialog-container': { justifyContent: 'flex-end' } }} slotProps={{ paper: { sx: { height: '100dvh', maxHeight: '100dvh', maxWidth: '100vw', flexShrink: 0, m: 0, width: { xs: '100%', sm: 640 }, borderRadius: 0 } } }}>
    <DialogTitle component="div" id="access-list-entries-heading" sx={{ display: 'flex', alignItems: 'center' }}><Typography component="h2" variant="h6" id="access-list-entries-title" sx={{ flex: 1, overflowWrap: 'anywhere' }}>{query.data?.name ?? t(`${P}title`)}</Typography><IconButton disabled={busy} onClick={onClose} aria-label={t('common:actions.close')}><CloseIcon /></IconButton></DialogTitle>
    <DialogContent dividers>{query.data ? <Stack spacing={2}>
      {query.error && <Alert severity="warning" action={<Button onClick={() => void query.refetch()}>{t('common:actions.retry')}</Button>}>{t(`${P}stale`)}</Alert>}
      <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>{t(`admin:access_control.lists.${query.data.kind}`)} · {query.data.kind === 'remote' ? query.data.source_url : query.data.geosite_category}</Typography>
      <Stack direction="row" sx={{ flexWrap: 'wrap', gap: 1 }}>{Object.entries(counts).map(([kind, count]) => <Chip key={kind} label={`${kind} ${count}`} clickable variant={type === kind ? 'filled' : 'outlined'} onClick={() => setType(type === kind ? null : kind)} />)}</Stack>
      <Typography variant="caption" color="text.secondary">{t(`${P}${query.data.entry_types ? 'bounded_totals' : 'bounded'}`, { count: query.data.entry_count, shown: entries.length })}</Typography>
      <TextField label={t(`${P}search`)} value={search} onChange={e => setSearch(e.target.value)} />
      <Box sx={{ minHeight: 80 }}>{filtered.map(entry => {
        const target = /^(domain|full):/.test(entry) ? entry.replace(/^[^:]+:/, '') : /^\d+\.\d+\.\d+\.\d+\/32$/.test(entry) || /^[a-f\d:]+\/128$/i.test(entry) ? entry.replace(/\/\d+$/, '') : null
        return <Stack key={entry} direction="row" sx={{ alignItems: 'center', gap: 1 }}><Typography variant="body2" sx={{ flex: 1, minWidth: 0, fontFamily: 'monospace', overflowWrap: 'anywhere', py: .5 }}>{entry}</Typography>{onTest && target && <Button disabled={busy} size="small" onClick={() => onTest(target)} aria-label={t('admin:access_control.test.test_entry', { name: entry })}>{t('admin:access_control.test.submit')}</Button>}</Stack>
      })}</Box>
      <Stack direction="row" spacing={1}><Button disabled={busy} onClick={onEdit}>{t('common:actions.edit')}</Button>{query.data.kind !== 'custom' && <Button disabled={busy || refreshing || query.data.state === 'refreshing'} onClick={onRefresh}>{t('admin:access_control.lists.refresh_now')}</Button>}</Stack>
      <ParseReport report={query.data.parse_report} kind={query.data.kind} />
    </Stack> : query.error ? <Alert severity="error" action={<Button onClick={() => void query.refetch()}>{t('common:actions.retry')}</Button>}>{t(`${P}failed`)}</Alert> : <Skeleton height={300} />}</DialogContent>
  </Dialog>
}
