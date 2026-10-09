import { useEffect, useRef, useState } from 'react'
import { Alert, Box, Button, Drawer, IconButton, Menu, MenuItem, Skeleton, Stack, Tooltip, Typography, useTheme } from '@mui/material'
import CloseIcon from '@mui/icons-material/Close'
import MoreHorizIcon from '@mui/icons-material/MoreHoriz'
import { useAccessTranslation } from '@/views/admin/accessControl/useAccessTranslation'
import type { DestinationExemptionView } from '@/api/accessControl'
import { useDestinationExemptions, useDeleteDestinationExemption } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { ToneBadge, stateTone } from '@/components/ToneBadge'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'
import { agoText } from '@/utils/riskCenter'
import PendingActionGuard from '../PendingActionGuard'
import { destinationError } from '../errors'
import { cancelExemptionCopy } from '../confirmCopy'
import AddExemptionDialog from './AddExemptionDialog'
const P = 'admin:access_control.exemptions.'
export default function ExemptionsSheet({ onClose, onOpenUser, etaMs }: { onClose: () => void; onOpenUser: (id: number) => void; etaMs?: number }) {
  const { t, dateTime } = useAccessTranslation(['admin', 'common']), theme = useTheme(), scope = useQueryScope()
  const query = useDestinationExemptions(scope), remove = useDeleteDestinationExemption(scope)
  const [menu, setMenu] = useState<{ anchor: HTMLElement; row: DestinationExemptionView } | null>(null)
  const [editor, setEditor] = useState<{ existing?: DestinationExemptionView } | null>(null)
  const [busy, setBusy] = useState(false), admission = useRef(false), [error, setError] = useState('')
  const [clock, setClock] = useState(Date.now())
  useEffect(() => { const timer = window.setInterval(() => setClock(Date.now()), 60000); return () => window.clearInterval(timer) }, [])
  const now = Math.max(clock, query.dataUpdatedAt)
  const expired = (row: DestinationExemptionView) => row.expired || row.expires_at !== null && row.expires_at <= now
  const rows = [...(query.data?.items ?? [])].sort((a, b) => Number(expired(a)) - Number(expired(b)))
  const cancel = async (row: DestinationExemptionView) => {
    if (admission.current) return
    admission.current = true; setBusy(true); setError('')
    try {
      if (!(await confirm(cancelExemptionCopy(t, row.upn ?? `#${row.user_id}`, etaMs)))) return
      await remove.mutateAsync(row.user_id); pushSnack(t(`${P}canceled`, { upn: row.upn ?? `#${row.user_id}` }), 'success')
    } catch (err) { setError(destinationError(err).error) } finally { admission.current = false; setBusy(false) }
  }
  const close = () => { if (!admission.current && !editor) onClose() }
  return <Drawer anchor="right" open onClose={close} slotProps={{ paper: { role: 'dialog', 'aria-labelledby': 'exemptions-sheet-title', sx: { width: { xs: '100vw', sm: 560 }, maxWidth: '100vw', bgcolor: theme.palette.md.surfaceContainerLow, borderTopLeftRadius: { xs: 0, sm: 16 }, display: 'flex', flexDirection: 'column' } } }}>
    {busy && <PendingActionGuard />}
    <Box sx={{ p: 2.5, display: 'flex', alignItems: 'center' }}><Typography id="exemptions-sheet-title" component="h2" variant="h6" sx={{ flex: 1 }}>{t(`${P}title`)}</Typography><IconButton disabled={busy || !!editor} aria-label={t('common:actions.close')} onClick={close} sx={{ minWidth: 44, minHeight: 44 }}><CloseIcon /></IconButton></Box>
    <Stack spacing={2} sx={{ px: 2.5, pb: 2.5, overflowY: 'auto', flex: 1 }}>
      <Typography variant="body2">{t(`${P}hint`)}</Typography><Button disabled={busy} sx={{ alignSelf: 'flex-end', minHeight: 44 }} onClick={() => setEditor({})}>{t(`${P}add`)}</Button>
      {error && <Alert severity="error">{t(`${P}cancel_failed`, { error })}</Alert>}
      {query.error && <Alert severity="error" action={<Button onClick={() => void query.refetch()} sx={{ minHeight: 44 }}>{t('common:actions.retry')}</Button>}>{t(`${P}load_failed`)}</Alert>}
      {!query.data && query.isPending && <Box aria-busy="true">{[0, 1, 2].map(key => <Skeleton key={key} variant="rounded" height={72} sx={{ mb: 1 }} />)}</Box>}
      {query.data && rows.length === 0 && <Typography color="text.secondary">{t(`${P}empty`)}</Typography>}
      {rows.map(row => {
        const name = row.upn ?? t(`${P}deleted_account`, { id: row.user_id }), isExpired = expired(row), left = row.expires_at === null ? null : Math.max(0, row.expires_at - now)
        const label = isExpired ? t(`${P}expired`) : left === null ? t(`${P}expiry_never`) : t(`${P}${left >= 3600000 ? 'expiry_hours' : 'expiry_minutes'}`, { count: Math.ceil(left / (left >= 3600000 ? 3600000 : 60000)) })
        return <Box key={row.user_id} sx={{ border: `1px solid ${theme.palette.md.outlineVariant}`, borderRadius: 2, p: 1.5 }}>
          <Stack direction="row" sx={{ alignItems: 'center', flexWrap: 'wrap', gap: 1 }}><Typography sx={{ flex: '1 1 auto', minWidth: 0, overflowWrap: 'anywhere' }}>{name}</Typography><Tooltip title={t(`${P}expiry_delay`)}><Box component="span"><ToneBadge data={isExpired ? 'expired' : left === null ? 'permanent' : 'active'} tone={stateTone(theme, isExpired ? 'measuring' : left !== null && left <= 86400000 ? 'attention' : 'quiet')} label={label} /></Box></Tooltip><IconButton disabled={busy} aria-label={t(`${P}menu`, { upn: name })} onClick={event => setMenu({ anchor: event.currentTarget, row })} sx={{ minWidth: 44, minHeight: 44 }}><MoreHorizIcon /></IconButton></Stack>
          <Typography variant="body2" color="text.secondary" sx={{ overflowWrap: 'anywhere' }}>{row.reason}</Typography><Typography variant="caption" title={dateTime(row.created_at)} color="text.secondary">{t(`${P}created`, { by: row.created_by_upn ?? `#${row.created_by}`, ago: agoText(Math.max(0, (now - row.created_at) / 1000), t) })}</Typography>
        </Box>
      })}
    </Stack>
    <Menu anchorEl={menu?.anchor} open={!!menu} onClose={() => setMenu(null)} slotProps={{ list: { sx: { '& .MuiMenuItem-root': { minHeight: 44 } } } }}>{menu && [<MenuItem key="account" onClick={() => { const id = menu.row.user_id; setMenu(null); onOpenUser(id) }}>{t(`${P}open_account`)}</MenuItem>, <MenuItem key="edit" onClick={() => { const row = menu.row; setMenu(null); setEditor({ existing: row }) }}>{t(`${P}edit`)}</MenuItem>, <MenuItem key="cancel" onClick={() => { const row = menu.row; setMenu(null); void cancel(row) }}>{t(`${P}cancel`)}</MenuItem>]}</Menu>
    {editor && <AddExemptionDialog key={editor.existing?.user_id ?? 'new'} existing={editor.existing} etaMs={etaMs} onClose={() => setEditor(null)} />}
  </Drawer>
}
