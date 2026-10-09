import { useRef, useState, type ReactNode } from 'react'
import { Alert, Box, Button, Dialog, DialogActions, DialogContent, DialogTitle, IconButton, Skeleton, Stack, Typography, useMediaQuery, useTheme } from '@mui/material'
import CloseIcon from '@mui/icons-material/Close'
import { isAxiosError } from 'axios'
import { useAccessTranslation } from '@/views/admin/accessControl/useAccessTranslation'
import type { AccessControlSettingKey, AccessControlSettingsView } from '@/api/accessControl'
import PolicyField from '@/components/PolicyField'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'
import { AsyncButton } from '@/components/AsyncButton'
import { useDirtyClose } from '@/hooks/useDirtyClose'
import { useLeaveGuard } from '@/hooks/useLeaveGuard'
import { useQueryScope } from '@/query/useQueryScope'
import { useAccessControlSettings, useSaveAccessControlSettings } from '@/query/accessControl'
import { scopeKey, type QueryScope } from '@/query/session'
import { useCan } from '@/utils/permissions'
import { ACCESS_SETTINGS_FIELDS, changedAccessSettings, shortenedRetentionDays, validateAccessSettings } from './settingsDraft'
import { discardSettingsCopy, shortenRetentionCopy } from './confirmCopy'

const P = 'admin:access_control.settings.'
interface Props { open: boolean; onClose: () => void; focusKey?: AccessControlSettingKey }
function errorText(error: unknown): string {
  if (isAxiosError(error)) return String(error.response?.data?.error ?? error.message)
  return error instanceof Error ? error.message : String(error)
}

function Frame({ children, actions, onClose, onEntered, busy = false }: { children: ReactNode; actions?: ReactNode; onClose: () => void; onEntered?: () => void; busy?: boolean }) {
  const { t } = useAccessTranslation(['admin', 'common'])
  const theme = useTheme()
  const fullScreen = useMediaQuery(theme.breakpoints.down('sm'))
  return <Dialog open fullWidth maxWidth="sm" fullScreen={fullScreen} onClose={() => { if (!busy) onClose() }}
    slotProps={{ transition: { onEntered }, paper: { sx: { '& button': { minWidth: 44, minHeight: 44 } } } }} aria-labelledby="access-settings-title">
    <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
      <DialogTitle id="access-settings-title" sx={{ flex: 1, minWidth: 0 }}>{t(`${P}title`)}</DialogTitle>
      <IconButton aria-label={t('common:actions.close')} onClick={onClose} disabled={busy} sx={{ mr: 2, width: 44, height: 44 }}><CloseIcon /></IconButton>
    </Box>
    <DialogContent dividers>{children}</DialogContent>
    <DialogActions sx={{ position: 'sticky', bottom: 0, bgcolor: 'background.paper', flexWrap: 'wrap', gap: 1, px: 3, py: 2 }}>
      {actions ?? <Button onClick={onClose}>{t('common:actions.close')}</Button>}
    </DialogActions>
  </Dialog>
}

export default function AccessSettingsDialog(props: Props) {
  const scope = useQueryScope()
  const canWrite = useCan('config.write')
  const { t } = useAccessTranslation('admin')
  if (!props.open) return null
  if (!canWrite) return <Frame onClose={props.onClose}><Alert severity="error">{t(`${P}forbidden`)}</Alert></Frame>
  return <SettingsRead key={scopeKey(scope)} {...props} scope={scope} />
}

function SettingsRead({ scope, ...props }: Props & { scope: QueryScope }) {
  const { t } = useAccessTranslation(['admin', 'common'])
  const q = useAccessControlSettings(scope, true)
  if (q.data) return <SettingsEditor {...props} scope={scope} loaded={q.data} />
  if (q.error) return <Frame onClose={props.onClose}>
    {isAxiosError(q.error) && q.error.response?.status === 503
      ? <Alert severity="info">{t(`${P}unwired`)}</Alert>
      : <Alert severity="error" action={<AsyncButton color="inherit" sx={{ whiteSpace: 'nowrap' }} pending={q.isFetching} onClick={() => q.refetch()}>{t('common:actions.retry')}</AsyncButton>}>
        {t(`${P}load_failed`, { error: errorText(q.error) })}
      </Alert>}
  </Frame>
  return <Frame onClose={props.onClose}><Stack spacing={2} aria-busy="true">
    {ACCESS_SETTINGS_FIELDS.map(({ key }) => <Skeleton key={key} variant="rounded" height={76} />)}
  </Stack></Frame>
}

function SettingsEditor({ scope, loaded, onClose, focusKey }: Props & { scope: QueryScope; loaded: AccessControlSettingsView }) {
  const { t } = useAccessTranslation(['admin', 'common'])
  const [baseline, setBaseline] = useState(loaded)
  const [draft, setDraft] = useState(loaded.settings)
  const [serverErrors, setServerErrors] = useState<Partial<Record<AccessControlSettingKey, string>>>({})
  const [submitting, setSubmitting] = useState(false)
  const admission = useRef(false)
  const fields = useRef<HTMLFieldSetElement>(null)
  const save = useSaveAccessControlSettings(scope)
  const changed = changedAccessSettings(draft, baseline.settings)
  const count = Object.keys(changed).length
  const errors = validateAccessSettings(draft, loaded.defaults)
  const busy = submitting || save.isPending
  const dirty = count > 0
  const mayClose = useDirtyClose(dirty, discardSettingsCopy(t))
  useLeaveGuard(dirty, discardSettingsCopy(t), (next, current) => next.pathname !== current.pathname || next.search !== current.search, busy)
  const close = async () => {
    if (busy || admission.current) return
    admission.current = true
    try {
      if (await mayClose()) {
        setDraft(baseline.settings)
        onClose()
      }
    } finally { admission.current = false }
  }
  const submit = async () => {
    if (admission.current || !dirty || Object.keys(errors).length || Object.keys(serverErrors).length) return
    admission.current = true
    setSubmitting(true)
    try {
      const days = shortenedRetentionDays(draft, baseline)
      if (days !== null) {
        const ok = await confirm(shortenRetentionCopy(t, days))
        if (!ok) return
      }
      const view = await save.mutateAsync(changed)
      setBaseline(view); setDraft(view.settings); setServerErrors({})
      pushSnack(t(`${P}saved`), 'success')
    } catch (error) {
      const body = isAxiosError(error) && error.response?.status === 400 ? error.response.data as { errors?: Record<string, unknown> } : undefined
      const mapped = Object.fromEntries(ACCESS_SETTINGS_FIELDS.filter(({ key }) => typeof body?.errors?.[key] === 'string')
        .map(({ key }) => [key, String(body!.errors![key])]))
      if (Object.keys(mapped).length) setServerErrors(mapped)
      else pushSnack(t(`${P}save_failed`, { error: errorText(error) }), 'error')
    } finally { admission.current = false; setSubmitting(false) }
  }
  return <Frame onClose={close} busy={busy} onEntered={() => {
    if (focusKey) fields.current?.querySelector<HTMLInputElement>(`[data-access-setting="${focusKey}"] input`)?.focus()
  }} actions={<>
    <Typography sx={{ flex: 1, color: 'text.secondary', fontSize: 13 }}>{t(`${P}changed`, { count })}</Typography>
    <Button disabled={busy} onClick={close}>{t(`${P}discard`)}</Button>
    <AsyncButton variant="contained" pending={busy} disabled={!dirty || Object.keys(errors).length > 0 || Object.keys(serverErrors).length > 0} onClick={submit}>
      {t(busy ? `${P}saving` : 'common:actions.save')}
    </AsyncButton>
  </>}>
    <Box component="fieldset" ref={fields} disabled={busy} sx={{ border: 0, m: 0, p: 0, minWidth: 0 }}>
      <Stack spacing={2.5}>
        {ACCESS_SETTINGS_FIELDS.map(({ key, min, max }, index) => <Box key={key} data-access-setting={key}>
          {[0, 3, 4].includes(index) && <Typography component="h3" variant="subtitle2" sx={{ mb: 2 }}>{t(`${P}${index === 0 ? 'retention' : index === 3 ? 'refresh' : 'apply'}`)}</Typography>}
          <PolicyField spec={{ key, label: `${P}${key}`, hint: key === 'dest_trial_retention_days' ? `${P}trial_hint` : undefined, min, max, tail: true, integerOnly: true }}
            value={draft[key]} defaults={loaded.defaults}
            error={serverErrors[key] ?? (errors[key] ? t(`${P}${errors[key]}`, { min, max }) : undefined)}
            onChange={value => {
              setDraft(old => ({ ...old, [key]: value as number }))
              setServerErrors(old => { const next = { ...old }; delete next[key]; return next })
            }} />
        </Box>)}
        <Typography sx={{ color: 'text.secondary', fontSize: 13 }}>{t(`${P}apply_hint`)}</Typography>
      </Stack>
    </Box>
  </Frame>
}
