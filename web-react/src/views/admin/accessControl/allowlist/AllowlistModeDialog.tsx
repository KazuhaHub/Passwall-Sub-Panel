import { useId, useRef, useState } from 'react'
import { Alert, Box, Button, Dialog, DialogActions, DialogContent, DialogTitle, Skeleton, Stack, Typography, useMediaQuery, useTheme } from '@mui/material'
import CheckCircleOutlineIcon from '@mui/icons-material/CheckCircleOutlineOutlined'
import ErrorOutlineIcon from '@mui/icons-material/ErrorOutlineOutlined'
import WarningAmberIcon from '@mui/icons-material/WarningAmber'
import { AsyncButton } from '@/components/AsyncButton'
import { pushSnack } from '@/components/SnackbarHost'
import { stateTone } from '@/components/ToneBadge'
import { useDestinationGroup, useSaveDestinationGroup } from '@/query/destinationGroups'
import type { QueryScope } from '@/query/session'
import { destinationError } from '../errors'
import PendingActionGuard from '../PendingActionGuard'
import { useAccessTranslation } from '../useAccessTranslation'

const P = 'admin:access_control.allowlist_mode.'
export default function AllowlistModeDialog({ scope, groupID, target, retentionDays, etaMs, onClose, onReport, onNodes }: {
  scope: QueryScope; groupID: number; target: 'enforce' | 'trial' | 'open'; retentionDays?: number; etaMs?: number
  onClose: () => void; onReport: (groupID: number) => void; onNodes: (groupID: number) => void
}) {
  const { t } = useAccessTranslation(['admin', 'common']), theme = useTheme(), titleID = useId()
  const fullScreen = useMediaQuery(theme.breakpoints.down('sm'))
  const read = useDestinationGroup(scope, groupID), save = useSaveDestinationGroup(scope)
  const admission = useRef(false)
  const [submitting, setSubmitting] = useState(false), [conflict, setConflict] = useState(false), [failed, setFailed] = useState(false)
  const data = read.data, readiness = data?.readiness, enforcing = target === 'enforce'
  const busy = submitting || save.isPending
  const compatible = data?.mode === 'allowlist' && (target === 'open' || (enforcing ? data.stage === 'trial' : data.stage === 'enforce'))
  const blocked = !data || !readiness || !!read.error || read.isFetching || !read.isFetchedAfterMount || !compatible || conflict ||
    enforcing && (!readiness.lists_ready || data.eligible_nodes === 0 || readiness.unapplied_nodes.length > 0 || data.applied_nodes !== data.eligible_nodes)
  const review = async () => { const fresh = await read.refetch(); if (!fresh.error && fresh.data) { setConflict(false); setFailed(false) } }
  const close = () => { if (!busy && !admission.current) onClose() }
  const submit = async () => {
    if (admission.current || busy || blocked || !data) return
    admission.current = true; setSubmitting(true); setFailed(false)
    try {
      const common = { list_ids: [...data.list_ids], updated_at: data.updated_at }
      await save.mutateAsync({ groupID, input: target === 'open' ? { ...common, mode: 'open', stage: '' } : { ...common, mode: 'allowlist', stage: target } })
      pushSnack(t(`${P}saved_${target}`, { name: data.group_name }), 'success')
      onClose()
    } catch (error) {
      const stale = destinationError(error).status === 409
      if (stale) setConflict(true); else setFailed(true)
      pushSnack(t(`${P}${stale ? 'conflict' : 'save_failed'}`), 'error')
    }
    finally { admission.current = false; setSubmitting(false) }
  }
  const check = (key: string, ok: boolean, detail?: string, warning = false) => {
    const tone = stateTone(theme, ok ? 'ok' : warning ? 'attention' : 'failing'), Icon = ok ? CheckCircleOutlineIcon : warning ? WarningAmberIcon : ErrorOutlineIcon
    return <Box sx={{ display: 'flex', gap: 1, p: 1.5, borderRadius: 2, bgcolor: tone.bg, color: tone.fg }}><Icon aria-hidden sx={{ color: tone.iconColor ?? tone.fg }} /><Box sx={{ minWidth: 0 }}><Typography variant="body2">{t(`${P}${key}`)}</Typography>{detail && <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>{detail}</Typography>}</Box></Box>
  }
  const title = t(`${P}title_${target}`, { name: data?.group_name ?? t(`${P}unknown_group`) })
  return <Dialog open fullWidth maxWidth="sm" fullScreen={fullScreen} aria-labelledby={titleID} onClose={close} slotProps={{ paper: { sx: { '& button': { minHeight: 44 } } } }}>
    <PendingActionGuard hold={busy} />
    <DialogTitle id={titleID}>{title}</DialogTitle>
    <DialogContent dividers><Stack spacing={1.5}>
      {conflict && <Alert severity="warning" action={<AsyncButton pending={read.isFetching} onClick={review}>{t(`${P}review`)}</AsyncButton>}>{t(`${P}conflict`)}</Alert>}
      {failed && <Alert severity="error">{t(`${P}save_failed`)}</Alert>}
      {read.error ? <Alert severity="error" action={<AsyncButton pending={read.isFetching} onClick={review}>{t('common:actions.retry')}</AsyncButton>}>{t(`${P}read_failed`)}</Alert> : !data ? <Stack spacing={1} aria-busy="true">{[0, 1, 2].map(key => <Skeleton key={key} variant="rounded" height={56} />)}</Stack> : <>
        {!compatible && <Alert severity="warning">{t(`${P}incompatible`)}</Alert>}
        {enforcing && readiness && <>
          {check(readiness.lists_ready ? 'lists_ready' : 'lists_unready', readiness.lists_ready, readiness.not_ready_lists.map(list => list.name).join(' · '))}
          {check(data.eligible_nodes === 0 ? 'zero_nodes' : readiness.unapplied_nodes.length > 0 ? 'unapplied' : 'nodes_ready', data.eligible_nodes > 0 && readiness.unapplied_nodes.length === 0, readiness.unapplied_nodes.map(node => node.name).join(' · '))}
          <Button disabled={busy} onClick={() => onNodes(groupID)}>{t(`${P}nodes`)}</Button>
          {check(readiness.base_nonempty ? 'base_nonempty' : 'base_empty', readiness.base_nonempty, undefined, true)}
          {readiness.remaining_sites_24h > 0 && check('remaining', false, t(`${P}remaining_detail`, { count: readiness.remaining_sites_24h, sites: readiness.top_sites.map(site => site.site).join(' · ') }), true)}
          <Button disabled={busy} onClick={() => onReport(groupID)}>{t(`${P}report`)}</Button>
          {data.stage_changed_at !== null && retentionDays !== undefined && <Typography variant="body2" color="text.secondary">{t(`${P}trial_age`, { days: Math.max(0, Math.floor((Date.now() - data.stage_changed_at) / 86400000)), recommend: Math.min(7, retentionDays) })}</Typography>}
        </>}
        <Box sx={{ bgcolor: enforcing ? theme.palette.md.errorContainer : theme.palette.md.surfaceContainerHigh, color: enforcing ? theme.palette.md.onErrorContainer : theme.palette.md.onSurface, p: 2, borderRadius: 2 }}>
          <Typography variant="body2">{t(`${P}impact_${target}`, { members: data.members })}</Typography>
          <Typography variant="body2" sx={{ mt: .5 }}>{etaMs ? t(`${P}eta`, { minutes: Math.ceil(etaMs / 60000) }) : t(`${P}eta_unknown`)}</Typography>
        </Box>
      </>}
    </Stack></DialogContent>
    <DialogActions sx={{ p: 2, gap: 1 }}><Button disabled={busy} onClick={close}>{t('common:actions.cancel')}</Button><AsyncButton variant="contained" color={enforcing ? 'error' : 'primary'} disabled={blocked} pending={busy} onClick={submit}>{t(`${P}confirm_${target}`)}</AsyncButton></DialogActions>
  </Dialog>
}
