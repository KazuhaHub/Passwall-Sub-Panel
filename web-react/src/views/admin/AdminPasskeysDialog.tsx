import { useEffect, useState } from 'react'
import {
  Box,
  Button,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  IconButton,
  List,
  ListItem,
  ListItemText,
  Tooltip,
  Typography,
} from '@mui/material'
import DeleteIcon from '@mui/icons-material/DeleteOutlined'
import { useTranslation } from 'react-i18next'
import type { AxiosError } from 'axios'

import { listUserPasskeys, revokeAllUserPasskeys, revokeUserPasskey } from '@/api/users'
import type { PasskeyCredential, User } from '@/api/types'
import type { M3Tokens } from '@/theme'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'
import { formatDualDate } from '@/utils/datetime'
import { useSiteStore } from '@/stores/site'

interface Props {
  open: boolean
  user: User | null
  md: M3Tokens
  onClose: () => void
}

// AdminPasskeysDialog lets an admin view and revoke a user's registered passkeys
// (break-glass for a lost/compromised device). Admins can't enroll on a user's
// behalf — that needs the user's own authenticator — so this is view + revoke
// only. The list is fetched lazily when the dialog opens.
export default function AdminPasskeysDialog({ open, user, md, onClose }: Props) {
  const { t } = useTranslation('admin')
  const panelTz = useSiteStore(s => s.timezone)
  const [loading, setLoading] = useState(false)
  const [busy, setBusy] = useState(false)
  const [creds, setCreds] = useState<PasskeyCredential[]>([])

  useEffect(() => {
    if (!open || !user) return
    setCreds([])
    setLoading(true)
    listUserPasskeys(user.id)
      .then(setCreds)
      .catch((e: AxiosError<{ error?: string }>) =>
        pushSnack(e.response?.data?.error || t('users.passkeys.load_failed', { defaultValue: '加载通行密钥失败' }), 'error'))
      .finally(() => setLoading(false))
  }, [open, user, t])

  function errSnack(e: unknown) {
    pushSnack((e as AxiosError<{ error?: string }>).response?.data?.error
      || t('users.passkeys.revoke_failed', { defaultValue: '吊销失败' }), 'error')
  }

  async function revokeOne(c: PasskeyCredential) {
    if (!user) return
    const ok = await confirm({
      title: t('users.passkeys.revoke_title', { defaultValue: '吊销通行密钥' }),
      message: t('users.passkeys.revoke_message', {
        name: c.name, upn: user.upn,
        defaultValue: '将吊销 {{upn}} 的通行密钥「{{name}}」。该设备将无法再用于登录。',
      }),
      destructive: true,
      confirmText: t('users.passkeys.revoke', { defaultValue: '吊销' }),
    })
    if (!ok) return
    setBusy(true)
    try {
      await revokeUserPasskey(user.id, c.id)
      setCreds(prev => prev.filter(x => x.id !== c.id))
      pushSnack(t('users.passkeys.revoked', { defaultValue: '已吊销通行密钥' }), 'success')
    } catch (e) {
      errSnack(e)
    } finally {
      setBusy(false)
    }
  }

  async function revokeAll() {
    if (!user || creds.length === 0) return
    const ok = await confirm({
      title: t('users.passkeys.revoke_all_title', { defaultValue: '吊销全部通行密钥' }),
      message: t('users.passkeys.revoke_all_message', {
        upn: user.upn, count: creds.length,
        defaultValue: '将吊销 {{upn}} 的全部 {{count}} 个通行密钥。仅在账号丢失全部设备或疑似被盗时使用。',
      }),
      destructive: true,
      confirmText: t('users.passkeys.revoke_all', { defaultValue: '全部吊销' }),
    })
    if (!ok) return
    setBusy(true)
    try {
      const n = await revokeAllUserPasskeys(user.id)
      setCreds([])
      pushSnack(t('users.passkeys.revoked_all', { count: n, defaultValue: '已吊销 {{count}} 个通行密钥' }), 'success')
    } catch (e) {
      errSnack(e)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onClose={() => !busy && onClose()} fullWidth maxWidth="xs"
      slotProps={{
        paper: { sx: { bgcolor: md.surfaceContainerHigh } }
      }}>
      <DialogTitle>
        {t('users.passkeys.title', { defaultValue: '通行密钥' })}{user ? ` — ${user.upn}` : ''}
      </DialogTitle>
      <DialogContent>
        <Typography variant="body2" sx={{ color: md.onSurfaceVariant, mb: 1.5 }}>
          {t('users.passkeys.intro', { defaultValue: '管理员可查看并吊销该用户的通行密钥（WebAuthn）。无法代为注册——注册需要用户本人的设备。' })}
        </Typography>
        {loading ? (
          <Box sx={{ display: 'grid', placeItems: 'center', py: 4 }}><CircularProgress size={24} /></Box>
        ) : creds.length === 0 ? (
          <Typography variant="body2" sx={{ color: md.onSurfaceVariant, py: 2, textAlign: 'center' }}>
            {t('users.passkeys.empty', { defaultValue: '该用户尚未注册任何通行密钥。' })}
          </Typography>
        ) : (
          <List dense disablePadding>
            {creds.map(c => (
              <ListItem key={c.id} disableGutters
                secondaryAction={
                  <Tooltip title={t('users.passkeys.revoke', { defaultValue: '吊销' })}>
                    <span>
                      <IconButton edge="end" onClick={() => revokeOne(c)} disabled={busy}>
                        <DeleteIcon fontSize="small" />
                      </IconButton>
                    </span>
                  </Tooltip>
                }>
                <ListItemText
                  primary={c.name}
                  secondary={
                    t('users.passkeys.added_at', { date: formatDualDate(c.created_at, panelTz), defaultValue: '添加于 {{date}}' })
                    + (c.last_used_at
                      ? ' · ' + t('users.passkeys.last_used', { date: formatDualDate(c.last_used_at, panelTz), defaultValue: '最近使用 {{date}}' })
                      : '')
                  }
                  slotProps={{
                    primary: { sx: { fontSize: 14 } },
                    secondary: { sx: { fontSize: 12 } }
                  }} />
              </ListItem>
            ))}
          </List>
        )}
      </DialogContent>
      <DialogActions>
        {creds.length > 0 && (
          <Button color="error" onClick={revokeAll} disabled={busy}
            startIcon={busy ? <CircularProgress size={14} color="inherit" /> : undefined}>
            {t('users.passkeys.revoke_all', { defaultValue: '全部吊销' })}
          </Button>
        )}
        <Box sx={{ flex: 1 }} />
        <Button onClick={onClose} disabled={busy}>{t('users.passkeys.close', { defaultValue: '关闭' })}</Button>
      </DialogActions>
    </Dialog>
  );
}
