import { useEffect, useRef, useState } from 'react'
import { Alert, Box, Button, Skeleton, Stack, Tooltip, Typography, useTheme } from '@mui/material'
import { useAccessTranslation } from '../../accessControl/useAccessTranslation'
import { useQueryScope } from '@/query/useQueryScope'
import { useDestinationUserAccess, useDeleteDestinationExemption } from '@/query/accessControl'
import { ToneBadge, stateTone } from '@/components/ToneBadge'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'
import { agoText } from '@/utils/riskCenter'
import AddExemptionDialog from '../../accessControl/sheets/AddExemptionDialog'
import PendingActionGuard from '../../accessControl/PendingActionGuard'
import { destinationError } from '../../accessControl/errors'
import { cancelExemptionCopy } from '../../accessControl/confirmCopy'
const P = 'admin:access_control.account.'
const E = 'admin:access_control.exemptions.'
export default function AccessTab({ userId, upn }: { userId: number; upn: string }) {
  const { t, dateTime } = useAccessTranslation(['admin', 'common']), theme = useTheme(), scope = useQueryScope()
  const query = useDestinationUserAccess(scope, userId), remove = useDeleteDestinationExemption(scope)
  const [editor, setEditor] = useState(false), [busy, setBusy] = useState(false), admission = useRef(false), [error, setError] = useState('')
  const [clock, setClock] = useState(Date.now())
  const now = Math.max(clock, query.dataUpdatedAt)
  useEffect(() => { const timer = window.setInterval(() => setClock(Date.now()), 60000); return () => window.clearInterval(timer) }, [])
  const cancel = async () => {
    if (admission.current) return
    admission.current = true; setBusy(true); setError('')
    try {
      if (!(await confirm(cancelExemptionCopy(t, upn)))) return
      await remove.mutateAsync(userId); pushSnack(t(`${E}canceled`, { upn }), 'success')
    } catch (err) { setError(destinationError(err).error) } finally { admission.current = false; setBusy(false) }
  }
  const data = query.data, exemption = data?.exemption
  const expired = exemption && (exemption.expired || exemption.expires_at !== null && exemption.expires_at <= now)
  return <Stack spacing={2}>
    {busy && <PendingActionGuard />}
    {query.error && <Alert severity="error" action={<Button disabled={query.isFetching} sx={{ minWidth: 44, minHeight: 44 }} onClick={() => void query.refetch()}>{t('common:actions.retry')}</Button>}>{t(`${P}load_failed`)}</Alert>}
    {!data && query.isPending && [0, 1].map(key => <Skeleton key={key} variant="rounded" height={64} />)}
    {error && <Alert severity="error">{t(`${E}cancel_failed`, { error })}</Alert>}
    {data && <>
      <Box><Typography variant="subtitle2">{t(`${P}group`)}</Typography><Stack direction="row" sx={{ gap: 1, flexWrap: 'wrap', alignItems: 'center' }}><Typography sx={{ overflowWrap: 'anywhere' }}>{data.group?.name ?? t(`${P}no_group`)}</Typography><ToneBadge tone={stateTone(theme, data.group?.mode === 'allowlist' ? 'measuring' : 'quiet')} label={t(`${P}${data.group?.mode === 'allowlist' ? data.group.stage === 'enforce' ? 'enforce' : 'trial' : 'unlimited'}`)} /></Stack></Box>
      <Box sx={{ p: 1.5, borderRadius: 2, bgcolor: theme.palette.md.surfaceContainerHigh }}><Typography variant="subtitle2">{t(`${P}exemption`)}</Typography>
        {exemption ? <Stack spacing={1}>
          <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>{exemption.reason}</Typography><Typography variant="caption">{t(`${E}created`, { by: exemption.created_by_upn ?? `#${exemption.created_by}`, ago: agoText(Math.max(0, (now - exemption.created_at) / 1000), t) })}</Typography>
          <Tooltip title={t(`${E}expiry_delay`)}><Box sx={{ alignSelf: 'flex-start' }}><ToneBadge tone={stateTone(theme, expired ? 'measuring' : 'quiet')} label={expired ? t(`${E}expired`) : exemption.expires_at === null ? t(`${E}expiry_never`) : t(`${P}expires_at`, { time: dateTime(exemption.expires_at) })} /></Box></Tooltip><Typography variant="caption">{t(`${E}allowlist_detail`)}</Typography><Button disabled={busy} sx={{ alignSelf: 'flex-start', minWidth: 44, minHeight: 44 }} onClick={() => void cancel()}>{t(`${E}cancel`)}</Button>
        </Stack> : <Stack direction="row" sx={{ alignItems: 'center', gap: 1, flexWrap: 'wrap' }}><Typography variant="body2">{t(`${P}not_exempt`)}</Typography><Button disabled={busy} sx={{ minWidth: 44, minHeight: 44 }} onClick={() => setEditor(true)}>{t(`${P}add`)}</Button></Stack>}
      </Box>
    </>}
    {editor && <AddExemptionDialog userId={userId} upn={upn} onClose={() => setEditor(false)} />}
  </Stack>
}
