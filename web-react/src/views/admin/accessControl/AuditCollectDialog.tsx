import { useEffect, useRef, useState } from 'react'
import { Alert, Box, Button, Dialog, DialogActions, DialogContent, DialogTitle, FormControlLabel, Radio, RadioGroup, Skeleton, Stack, Typography, useMediaQuery, useTheme } from '@mui/material'
import { Link as RouterLink } from 'react-router'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import type { DestinationNodeStatus } from '@/api/accessControl'
import { updateServer, type NativeAuditCollect } from '@/api/servers'
import { AsyncButton } from '@/components/AsyncButton'
import FieldHint from '@/components/FieldHint'
import { pushSnack } from '@/components/SnackbarHost'
import { useAccessControlSettings } from '@/query/accessControl'
import { useUISettings } from '@/query/settings'
import { accessControlKeys, serverKeys } from '@/query/keys'
import { scopeKey, type QueryScope } from '@/query/session'
import { useQueryScope } from '@/query/useQueryScope'
import { useCan } from '@/utils/permissions'
import { destinationError } from './errors'
import { useAccessTranslation } from './useAccessTranslation'
import DataDisclosure from './DataDisclosure'

const P = 'admin:access_control.collect.'
const modes: NativeAuditCollect[] = ['off', 'hits', 'hits_and_usage']
interface Props { node: DestinationNodeStatus; onClose: () => void }
const positive = (n: unknown): n is number => typeof n === 'number' && Number.isSafeInteger(n) && n > 0

export default function AuditCollectDialog(props: Props) {
  const scope = useQueryScope()
  const canWrite = useCan('config.write')
  // Drop drafts, pending read state and callbacks when the session changes.
  if (!canWrite || props.node.kind !== 'psp') return null
  return <Editor key={`${scopeKey(scope)}:${props.node.panel_id}`} {...props} scope={scope} />
}

function Editor({ node, onClose, scope }: Props & { scope: QueryScope }) {
  const { t } = useAccessTranslation(['admin', 'common'])
  const theme = useTheme()
  const narrow = useMediaQuery(theme.breakpoints.down('sm'))
  const queryClient = useQueryClient()
  const settings = useAccessControlSettings(scope, true)
  const ui = useUISettings(scope)
  const [selected, setSelected] = useState<NativeAuditCollect>(node.collect)
  const [error, setError] = useState('')
  const admission = useRef(false)
  const mounted = useRef(true)
  useEffect(() => { mounted.current = true; return () => { mounted.current = false } }, [])
  const save = useMutation({ mutationFn: (collect: NativeAuditCollect) => updateServer(node.panel_id, { audit_collect: collect }),
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: serverKeys.all(scope) })
      void queryClient.invalidateQueries({ queryKey: accessControlKeys.status(scope) })
    } })
  const effective = settings.data?.effective
  const hitDays = effective?.dest_hit_retention_days
  const usageDays = effective?.dest_usage_retention_days
  const poll = ui.data?.node_poll_seconds
  const ready = !settings.isError && !ui.isError && positive(hitDays) && positive(usageDays)
    && typeof poll === 'number' && Number.isSafeInteger(poll) && poll >= 0
  const readOnly = node.engine !== 'xray'
  const allowed = (mode: NativeAuditCollect) => !readOnly && (mode === 'off' || (node.supports.hits && (mode === 'hits' || node.supports.usage)))
  const busy = save.isPending
  const retry = async () => { await Promise.allSettled([settings.refetch(), ui.refetch()]) }
  const submit = async () => {
    if (admission.current || !ready || !allowed(selected) || selected === node.collect) return
    admission.current = true; setError('')
    try {
      await save.mutateAsync(selected)
      if (mounted.current) {
        pushSnack(t(`${P}saved`, { node: node.panel_name, minutes: Math.max(1, Math.ceil((poll || 30) / 60)) }), 'success')
        onClose()
      }
    } catch (error) { if (mounted.current) setError(destinationError(error).error) }
    finally { admission.current = false }
  }
  return <Dialog open fullScreen={narrow} fullWidth maxWidth="sm" aria-labelledby="audit-collect-title"
    onClose={() => { if (!busy && !admission.current) onClose() }} slotProps={{ paper: { sx: { bgcolor: theme.palette.md.surfaceContainerLow } } }}>
    <DialogTitle id="audit-collect-title" sx={{ overflowWrap: 'anywhere' }}>{t(`${P}title`, { node: node.panel_name })}</DialogTitle>
    <DialogContent dividers><Stack spacing={2}>
      {error && <Alert severity="error">{error}</Alert>}
      {readOnly && <Typography color="text.secondary">{t(`${P}${node.engine === 'sing-box' ? 'sing_box' : 'engine_unknown'}`)}</Typography>}
      {!readOnly && !node.supports.hits && <Typography color="text.secondary">{t(`${P}upgrade_hits`)}</Typography>}
      {!readOnly && node.supports.hits && !node.supports.usage && <Typography color="text.secondary">{t(`${P}upgrade_usage`)}</Typography>}
      {!ready ? settings.isPending || ui.isPending
        ? <Stack role="progressbar" aria-label={t(`${P}loading`)} spacing={1}>{modes.map(mode => <Skeleton key={mode} variant="rounded" height={88} />)}</Stack>
        : <Alert severity="error" action={<AsyncButton pending={settings.isFetching || ui.isFetching} onClick={retry}>{t('common:actions.retry')}</AsyncButton>}>{t(`${P}read_failed`)}</Alert>
        : <>
          <RadioGroup value={selected} aria-label={t(`${P}mode`)} onChange={event => { if (allowed(event.target.value as NativeAuditCollect) && !admission.current) setSelected(event.target.value as NativeAuditCollect) }}>
            {modes.map(mode => <FormControlLabel key={mode} value={mode} disabled={busy || !allowed(mode)}
              control={<Radio slotProps={{ input: { 'aria-label': t(`${P}${mode}`) } }} />}
              sx={{ mx: 0, mb: 1, p: 1.5, alignItems: 'flex-start', minHeight: 44, border: '1px solid', borderRadius: 2,
                borderColor: selected === mode ? theme.palette.md.primary : theme.palette.md.outlineVariant }}
              label={<Box component="span" sx={{ display: 'block', overflowWrap: 'anywhere', pt: 1 }}>
                <Typography component="span" sx={{ display: 'block', fontWeight: 600 }}>{t(`${P}${mode}`)}</Typography>
                <Typography component="span" variant="body2" sx={{ display: 'block', color: 'text.secondary' }}>{t(`${P}${mode}_detail`, { hitDays, usageDays })}</Typography>
              </Box>} />)}
          </RadioGroup>
          <DataDisclosure usage={selected === 'hits_and_usage'} />
          {selected === 'hits_and_usage' && ui.data?.legal_enabled !== true && <Stack spacing={0.5}>
            <FieldHint tone="amber" summary={t(`${P}${ui.data?.legal_enabled === false ? 'legal_disabled' : 'legal_unknown'}`)} detail={t(`${P}legal_detail`)} />
            <Button component={RouterLink} to="/admin/settings?tab=legal" disabled={busy} sx={{ alignSelf: 'flex-start', minHeight: 44 }}>{t(`${P}open_legal`)}</Button>
          </Stack>}
        </>}
    </Stack></DialogContent>
    <DialogActions sx={{ px: 3, py: 2, flexWrap: 'wrap', gap: 1 }}>
      <Box sx={{ flex: '1 1 240px' }}><Typography variant="body2" color="text.secondary">{t(`${P}restart`)}</Typography>
        {selected === 'hits_and_usage' && positive(usageDays) && <Typography variant="body2" color="text.secondary">{t(`${P}usage_reminder`, { days: usageDays })}</Typography>}
      </Box>
      <Button disabled={busy} onClick={() => { if (!admission.current) onClose() }} sx={{ minHeight: 44 }}>{t('common:actions.cancel')}</Button>
      <AsyncButton variant="contained" pending={busy} disabled={!ready || !allowed(selected) || selected === node.collect} onClick={submit} sx={{ minHeight: 44 }}>{t('common:actions.save')}</AsyncButton>
    </DialogActions>
  </Dialog>
}
