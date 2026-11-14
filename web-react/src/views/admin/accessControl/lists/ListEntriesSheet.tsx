import { useState } from 'react'
import { Alert, Box, Button, Drawer, IconButton, InputAdornment, Skeleton, TextField, Tooltip, Typography, useTheme } from '@mui/material'
import CloseIcon from '@mui/icons-material/Close'
import SearchIcon from '@mui/icons-material/Search'
import { useTranslation } from 'react-i18next'

import { AsyncButton } from '@/components/AsyncButton'
import ToneBadge from '@/components/ToneBadge'
import { pushSnack } from '@/components/SnackbarHost'
import { useDestList, useDestPolicies, useRefreshDestList } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { accessTone } from '@/utils/accessControl'
import { reportDestError } from '../destErrors'
import { listStatus } from './listStatus'

const L = 'admin:access_control.lists.'

export interface ListEntriesSheetProps {
  /** The list the URL names, or null (closed). */
  listId: number | null
  onClose: () => void
  /** Opens the list's edit dialog (lists tab). */
  onEdit: (id: number) => void
  /** Opens the test sheet prefilled with an entry. */
  onTest: (target: string) => void
}

/**
 * A list's contents (S6 drawer): its figures and the first 200 canonical
 * entries. Page-level drawers keep the default z-index: a dialog opened from
 * one (the edit dialog, the test sheet) is a modal and so sits above it. The
 * search is the component's own state and never reaches the URL (R12); it
 * covers the 200 shown, not the whole list.
 */
export default function ListEntriesSheet({ listId, onClose, onEdit, onTest }: ListEntriesSheetProps) {
  const { t, i18n } = useTranslation(['admin'])
  const theme = useTheme()
  const md = theme.palette.md
  const scope = useQueryScope()
  const open = listId !== null
  const q = useDestList(scope, listId, { enabled: open })
  const policies = useDestPolicies(scope, { enabled: open })
  const refresh = useRefreshDestList(scope)
  const [search, setSearch] = useState('')

  const list = q.data?.list
  const enabledUsed = !!list && list.used_by.some(u => [...(policies.data?.allow ?? []), ...(policies.data?.block ?? [])].some(p => p.id === u.id && p.state !== 'off'))
  const st = list ? listStatus(list, enabledUsed) : null
  const needle = search.trim().toLowerCase()
  const shown = (q.data?.samples ?? []).filter(e => !needle || e.toLowerCase().includes(needle))
  const mono = { fontFamily: 'ui-monospace, monospace', fontSize: 13 }
  const num = new Intl.NumberFormat(i18n.language)

  const source = !list ? '' : list.kind === 'remote'
    ? t(`${L}sheet.source`, { url: list.source_url ?? '' })
    : list.kind === 'geosite' ? t(`${L}sheet.category`, { name: list.geosite_category ?? '' }) : t(`${L}sheet.custom`)

  return (
    <Drawer anchor="right" open={open} onClose={onClose}
      slotProps={{ paper: { sx: { width: { xs: '100vw', sm: 560 }, maxWidth: '100vw', bgcolor: md.surfaceContainerLow, borderTopLeftRadius: { sm: 16 } } } }}>
      <Box sx={{ p: 2, display: 'flex', flexDirection: 'column', gap: 1.5, minHeight: 0, height: '100%' }}>
        <Box sx={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 1 }}>
          <Box sx={{ minWidth: 0 }}>
            <Typography component="h2" sx={{ fontSize: 16, fontWeight: 600, overflowWrap: 'anywhere' }}>{list?.name ?? ''}</Typography>
            <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant, overflowWrap: 'anywhere' }}>{source}</Typography>
          </Box>
          <IconButton aria-label={t(`${L}sheet.close`)} onClick={onClose}><CloseIcon /></IconButton>
        </Box>
        {q.isPending && open && <Skeleton variant="rounded" height={160} />}
        {q.isError && <Alert severity="error" action={<Button color="inherit" size="small" onClick={() => void q.refetch()}>{t('admin:access_control.retry')}</Button>}>{t(`${L}sheet.load_failed`)}</Alert>}
        {list && st && (
          <>
            <Box sx={{ display: 'flex', gap: 1, alignItems: 'center', flexWrap: 'wrap' }}>
              <ToneBadge tone={accessTone(theme, st.tone)} label={t(`${L}state.${st.key}`)} data={st.key} />
              <Typography sx={{ fontSize: 13 }}>{t(`${L}sheet.counts`, { entries: num.format(list.entry_count), regexps: num.format(list.regexp_count) })}</Typography>
            </Box>
            <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant }}>{t(`${L}sheet.first200`, { count: num.format(list.entry_count) })}</Typography>
            <TextField size="small" value={search} onChange={e => setSearch(e.target.value)} placeholder={t(`${L}sheet.search`)}
              slotProps={{ htmlInput: { 'aria-label': t(`${L}sheet.search`) }, input: { startAdornment: <InputAdornment position="start"><SearchIcon fontSize="small" /></InputAdornment> } }} />
            <Box sx={{ flex: 1, minHeight: 0, overflow: 'auto' }}>
              {shown.length === 0 && <Typography sx={{ color: md.onSurfaceVariant }}>{t(`${L}sheet.${(q.data?.samples.length ?? 0) === 0 ? 'none' : 'no_match'}`)}</Typography>}
              {shown.map(e => (
                <Box key={e} sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 1, py: 0.5, borderBottom: `1px solid ${md.outlineVariant}` }}>
                  <Tooltip title={e}><Typography noWrap sx={{ ...mono, minWidth: 0 }}>{e}</Typography></Tooltip>
                  <Button size="small" sx={{ flex: '0 0 auto', minHeight: 36 }} onClick={() => onTest(e)}>{t(`${L}sheet.test`)}</Button>
                </Box>
              ))}
            </Box>
            <Box sx={{ display: 'flex', gap: 1 }}>
              {list.kind === 'remote' && (
                <AsyncButton variant="outlined" onClick={async () => {
                  try { await refresh.mutateAsync(list.id); pushSnack(t(`${L}snack.refresh_started`, { name: list.name }), 'info') } catch (err) { reportDestError(t, err) }
                }}>{t(`${L}sheet.refresh`)}</AsyncButton>
              )}
              {list.kind !== 'geosite' && <Button variant="outlined" onClick={() => onEdit(list.id)}>{t(`${L}sheet.edit`)}</Button>}
            </Box>
          </>
        )}
      </Box>
    </Drawer>
  )
}
