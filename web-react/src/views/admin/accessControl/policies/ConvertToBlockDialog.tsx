import { useRef, useState } from 'react'
import { Alert, Box, Button, Checkbox, Dialog, DialogActions, DialogContent, DialogTitle, FormControlLabel, IconButton, Stack, Typography, useMediaQuery, useTheme } from '@mui/material'
import CloseIcon from '@mui/icons-material/Close'
import { useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { getDestinationPolicies, type DestinationPoliciesView, type DestinationPolicyOverviewItem } from '@/api/accessControl'
import { pushSnack } from '@/components/SnackbarHost'
import { useDirtyClose } from '@/hooks/useDirtyClose'
import { useLeaveGuard } from '@/hooks/useLeaveGuard'
import { useSaveDestinationPolicy } from '@/query/accessControl'
import { accessControlKeys } from '@/query/keys'
import { useQueryScope } from '@/query/useQueryScope'
import { discardSettingsCopy } from '../confirmCopy'
import { destinationError } from '../errors'
import { policyInput } from './policyDraft'
const P = 'admin:access_control.promotion.'
export default function ConvertToBlockDialog({ policy, onClose }: { policy: DestinationPolicyOverviewItem; onClose: () => void }) {
  const { t } = useTranslation(['admin', 'common']), mobile = useMediaQuery(useTheme().breakpoints.down('sm')), scope = useQueryScope()
  const [row, setRow] = useState(policy), [risk, setRisk] = useState(false), [busy, setBusy] = useState(false), [error, setError] = useState('')
  const admission = useRef(false), client = useQueryClient(), save = useSaveDestinationPolicy(scope)
  const copy = discardSettingsCopy(t), checkClose = useDirtyClose(risk, copy)
  useLeaveGuard(risk, copy, (next, current) => next.pathname !== current.pathname || next.search !== current.search, busy)
  const close = async () => { if (!admission.current && await checkClose()) onClose() }
  const submit = async () => {
    if (admission.current || row.action !== 'observe' || error === 'dest_policy_stale') return
    admission.current = true; setBusy(true)
    try {
      await save.mutateAsync({ input: { ...policyInput(row), action: 'block', counts_as_risk: risk }, existing: row })
      const latest = client.getQueryData<DestinationPoliciesView>(accessControlKeys.policies(scope))
      const index = latest?.block.findIndex(item => item.id === row.id) ?? -1
      pushSnack(t(`${P}${index >= 0 ? 'saved_position' : 'saved'}`, { position: index + 1 }), 'success')
      setRisk(false); onClose()
    } catch (err) { setError(destinationError(err).error) } finally { admission.current = false; setBusy(false) }
  }
  const reload = async () => {
    if (admission.current) return
    admission.current = true; setBusy(true)
    try {
      const latest = await getDestinationPolicies({ silent: true })
      const next = [...latest.allow, ...latest.block, ...latest.observe].find(item => item.id === row.id)
      if (!next) { setError('dest_policy_missing'); return }
      setRow(next); setRisk(false); setError('')
    } catch (err) { setError(destinationError(err).error) } finally { admission.current = false; setBusy(false) }
  }
  return <Dialog open fullWidth maxWidth="sm" fullScreen={mobile} onClose={() => void close()} aria-labelledby="policy-promotion-title">
    <DialogTitle id="policy-promotion-title" sx={{ display: 'flex', alignItems: 'center' }}><Box component="span" sx={{ flex: 1 }}>{t(`${P}title`, { name: row.name })}</Box><IconButton disabled={busy} aria-label={t('common:actions.close')} onClick={() => void close()}><CloseIcon /></IconButton></DialogTitle>
    <DialogContent dividers><Stack spacing={2}>
      {error && <Alert severity="error" action={error === 'dest_policy_stale' ? <Button disabled={busy} onClick={() => void reload()}>{t(`${P}reload`)}</Button> : undefined}>{t(`${P}${error}`, { defaultValue: error })}</Alert>}
      {row.action !== 'observe' ? <Alert severity="info">{t(`${P}already_changed`)}</Alert> : <>
        <Typography variant="body2">{t(`${P}impact`)}</Typography>
        {!row.enabled && <Alert severity="info">{t(`${P}disabled`)}</Alert>}
        <FormControlLabel label={t(`${P}risk`)} control={<Checkbox checked={risk} disabled={busy} onChange={(_, value) => setRisk(value)} />} />
      </>}
    </Stack></DialogContent>
    <DialogActions sx={{ px: 3, py: 2 }}><Button disabled={busy} onClick={() => void close()}>{t('common:actions.cancel')}</Button>{row.action === 'observe' && <Button variant="contained" disabled={busy || error === 'dest_policy_stale'} onClick={() => void submit()}>{t(busy ? 'admin:access_control.settings.saving' : `${P}action`)}</Button>}</DialogActions>
  </Dialog>
}
