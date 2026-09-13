import { useState } from 'react'
import { Alert, Box, Button, Dialog, DialogActions, DialogContent, DialogTitle, Link, MenuItem, Stack, TextField, Typography } from '@mui/material'
import { useTranslation } from 'react-i18next'
import type { PanelType, Server } from '@/api/servers'
import { useCan } from '@/utils/permissions'

const BACKENDS = ['psp', '3xui', 'sui'] as const
const LABELS: Record<PanelType, string> = { psp: 'Passwall Node', '3xui': '3X-UI', sui: 'S-UI' }
const MANUAL_GUIDES = {
  '3xui': 'https://github.com/MHSanaei/3x-ui',
  sui: 'https://github.com/alireza0/s-ui',
} as const

interface ReinstallBackendDialogProps {
  server: Server | null
  onClose: () => void
  onNativeInstall: (server: Server) => void
  onNativeMigration: (server: Server) => void
  onConfigure: (server: Server) => void
}

export function ReinstallBackendDialog({ server, onClose, onNativeInstall, onNativeMigration, onConfigure }: ReinstallBackendDialogProps) {
  const { t } = useTranslation(['admin', 'common'])
  const canConfigure = useCan('config.write')
  // The parent keys this small dialog by server ID. An existing record always
  // defaults to its original backend; opening it must not change its adapter.
  const original = server?.panel_type ?? '3xui'
  const [target, setTarget] = useState<PanelType>(original)
  const sameBackend = target === original
  const nativeReinstall = sameBackend && target === 'psp'
  const nativeMigration = !sameBackend && original === '3xui' && target === 'psp'
  const manualReinstall = sameBackend && target !== 'psp'

  return <Dialog open={!!server} onClose={onClose} fullWidth maxWidth="sm">
    <DialogTitle>{t('admin:servers.install_reinstall.title', { name: server?.name ?? '' })}</DialogTitle>
    <DialogContent>
      <Stack spacing={2} sx={{ pt: 1 }}>
        {!canConfigure ? <Alert severity="error">{t('admin:servers.install_reinstall.forbidden')}</Alert> : <>
          <Typography>{t('admin:servers.install_reinstall.original', {
            id: server?.id, backend: LABELS[original],
          })}</Typography>
          <TextField select fullWidth label={t('admin:servers.install_reinstall.backend')} value={target}
            onChange={event => setTarget(event.target.value as PanelType)}>
            {BACKENDS.map(backend => <MenuItem key={backend} value={backend}>{LABELS[backend]}</MenuItem>)}
          </TextField>
          {sameBackend
            ? <Alert severity="info">{t(nativeReinstall
              ? 'admin:servers.install_reinstall.native_same'
              : 'admin:servers.install_reinstall.manual_same')}</Alert>
            : <Alert severity="warning">{t('admin:servers.install_reinstall.switch_warning', {
              original: LABELS[original], target: LABELS[target],
            })}</Alert>}
          {nativeMigration && <Typography>{t('admin:servers.install_reinstall.switch_precheck')}</Typography>}
          {manualReinstall && <>
            <TextField select fullWidth label={t('admin:servers.install_reinstall.method')} value="manual">
              <MenuItem value="manual">{t('admin:servers.install_reinstall.manual_method')}</MenuItem>
              <MenuItem value="automatic" disabled>{t('admin:servers.install_reinstall.automatic_unavailable')}</MenuItem>
            </TextField>
            <Alert severity="warning">{t('admin:servers.install_reinstall.manual_unverified')}</Alert>
            <Box component="ol" sx={{ my: 0, pl: 3, '& li + li': { mt: 1 } }}>
              <li>{t('admin:servers.install_reinstall.manual_backup')}</li>
              <li>{t('admin:servers.install_reinstall.manual_install', { backend: LABELS[target] })}</li>
              <li>{t('admin:servers.install_reinstall.manual_restore')}</li>
              <li>{t('admin:servers.install_reinstall.manual_configure')}</li>
            </Box>
            <Link href={MANUAL_GUIDES[target as '3xui' | 'sui']} target="_blank" rel="noopener noreferrer">
              {t('admin:servers.install_reinstall.official_guide', { backend: LABELS[target] })}
            </Link>
          </>}
          {!sameBackend && !nativeMigration && <Alert severity="error">
            {t('admin:servers.install_reinstall.switch_unavailable')}
          </Alert>}
        </>}
      </Stack>
    </DialogContent>
    <DialogActions>
      <Button onClick={onClose}>{t('common:actions.close')}</Button>
      {manualReinstall && canConfigure && <Button variant="outlined" onClick={() => server && onConfigure(server)}>
        {t('admin:servers.install_reinstall.configure_original')}
      </Button>}
      {!manualReinstall && <Button variant="contained" disabled={!canConfigure || (!nativeReinstall && !nativeMigration)}
        onClick={() => {
          if (!server || !canConfigure) return
          if (nativeReinstall) onNativeInstall(server)
          else if (nativeMigration) onNativeMigration(server)
        }}>
        {t(nativeMigration ? 'admin:servers.install_reinstall.precheck' : 'admin:servers.install_reinstall.continue')}
      </Button>}
    </DialogActions>
  </Dialog>
}
