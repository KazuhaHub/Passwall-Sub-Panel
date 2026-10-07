import { Alert, Stack } from '@mui/material'
import { useTranslation } from 'react-i18next'
import { AsyncButton } from '@/components/AsyncButton'

export default function GeositeDownloadNotice({ message, pending, disabled, failed, onDownload }: {
  message?: string
  pending: boolean
  disabled?: boolean
  failed?: boolean
  onDownload: () => Promise<void>
}) {
  const { t } = useTranslation('admin')
  return <Stack spacing={1}>
    <Alert severity="info">{message ?? t('admin:access_control.categories.missing')}</Alert>
    <AsyncButton sx={{ alignSelf: 'flex-start' }} pending={pending} disabled={disabled} onClick={onDownload}>{t('admin:access_control.categories.download')}</AsyncButton>
    {failed && <Alert severity="error">{t('admin:access_control.categories.failed')}</Alert>}
  </Stack>
}
