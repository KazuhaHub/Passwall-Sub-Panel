import { useState } from 'react'
import {
  Alert,
  Avatar,
  Box,
  Button,
  Chip,
  Drawer,
  FormControlLabel,
  IconButton,
  Stack,
  Switch,
  Typography,
} from '@mui/material'
import CloseIcon from '@mui/icons-material/Close'
import ContentCopyIcon from '@mui/icons-material/ContentCopy'
import LockResetIcon from '@mui/icons-material/LockReset'
import ShieldIcon from '@mui/icons-material/GppGood'
import FingerprintIcon from '@mui/icons-material/Fingerprint'
import KeyIcon from '@mui/icons-material/VpnKey'
import LinkOffIcon from '@mui/icons-material/LinkOff'
import LinkIcon from '@mui/icons-material/Link'
import EmergencyIcon from '@mui/icons-material/MedicalServices'
import PasswordIcon from '@mui/icons-material/Password'
import { useTranslation } from 'react-i18next'

import { regenerateUser2FARecovery, updateUser } from '@/api/users'
import type { User } from '@/api/types'
import type { M3Tokens } from '@/theme'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'

interface Props {
  open: boolean
  user: User | null
  md: M3Tokens
  onClose: () => void
  // The rich / dialog-backed actions stay owned by UsersView (proven confirm +
  // result flows); the drawer just calls them. Recovery-code regeneration is new
  // and self-contained here.
  onResetPassword: (u: User) => void
  onResetCredentials: (u: User) => void
  onResetEmergency: (u: User) => void
  onUnlinkSSO: (u: User) => void
  onReset2FA: (u: User) => void
  onManagePasskeys: (u: User) => void
}

// AccountSecurityDrawer is the single hub for a user's account-security actions —
// it replaces the half-dozen security items that used to crowd the row "more"
// menu. Admins can't ENROLL 2FA/passkeys (that needs the user's own device) —
// only reset / revoke / regenerate-codes.
export default function AccountSecurityDrawer({
  open, user, md, onClose,
  onResetPassword, onResetCredentials, onResetEmergency, onUnlinkSSO, onReset2FA, onManagePasskeys,
}: Props) {
  const { t } = useTranslation('admin')
  const [busy, setBusy] = useState(false)
  const [recoveryCodes, setRecoveryCodes] = useState<string[] | null>(null)
  const [require2FA, setRequire2FA] = useState(false)

  // Keep the Drawer permanently mounted (we no longer `return null` when there's
  // no user) so MUI runs its slide + backdrop-fade transitions BOTH ways. The old
  // unmount-on-close re-mounted the Drawer on every open, and MUI only animates
  // `appear` AFTER a component's first mount — so the enter was skipped and the
  // exit snapped. We snapshot the targeted user into `shown` so its content is
  // present on the first open frame and survives the close slide-out.
  const [shown, setShown] = useState<User | null>(null)
  if (user && user !== shown) setShown(user)

  // Reset transient UI on each open edge (also covers reopening the same user).
  const [wasOpen, setWasOpen] = useState(false)
  if (open !== wasOpen) {
    setWasOpen(open)
    if (open) {
      setRecoveryCodes(null)
      setRequire2FA(!!user?.require_2fa)
    }
  }

  const u = shown
  const hasSSO = !!u?.sso_provider && u.sso_provider !== 'local'
  const initial = (u?.display_name || u?.upn || '?').trim().charAt(0).toUpperCase()

  async function toggleRequire2FA(next: boolean) {
    if (!u) return
    setRequire2FA(next) // optimistic
    setBusy(true)
    try {
      await updateUser(u.id, { require_2fa: next })
      pushSnack(t('users.toast.saved', { defaultValue: '已保存' }), 'success')
    } catch {
      setRequire2FA(!next) // revert
    } finally { setBusy(false) }
  }

  async function copy(text: string) {
    try {
      await navigator.clipboard.writeText(text)
      pushSnack(t('users.toast.copied'), 'success')
    } catch { /* ignore */ }
  }

  async function doRegenRecovery() {
    if (!u) return
    const ok = await confirm({
      title: t('users.security.regen_recovery_title', { defaultValue: '重新生成备用码' }),
      message: t('users.security.regen_recovery_message', {
        upn: u.upn,
        defaultValue: '将作废 {{upn}} 现有的全部备用码并生成一组新的。请把新备用码通过安全渠道转交给用户——它只会显示这一次。',
      }),
      confirmText: t('users.security.regen_recovery_confirm', { defaultValue: '重新生成' }),
    })
    if (!ok) return
    setBusy(true)
    try {
      setRecoveryCodes(await regenerateUser2FARecovery(u.id))
    } finally { setBusy(false) }
  }

  return (
    <Drawer anchor="right" open={open} onClose={onClose}
      transitionDuration={{ enter: 300, exit: 240 }}
      PaperProps={{ sx: { width: 480, maxWidth: '94vw', bgcolor: md.surfaceContainerLow, borderTopLeftRadius: 16, borderBottomLeftRadius: 16 } }}>
      {u && (<>
      {/* Header */}
      <Box sx={{ p: 2.5, pb: 2, display: 'flex', alignItems: 'center', gap: 1.5 }}>
        <Avatar sx={{ bgcolor: md.primaryContainer, color: md.onPrimaryContainer, width: 44, height: 44, fontWeight: 700 }}>
          {initial}
        </Avatar>
        <Box sx={{ flex: 1, minWidth: 0 }}>
          <Typography variant="subtitle1" fontWeight={700} noWrap>{u.display_name || u.upn}</Typography>
          {u.display_name && <Typography variant="caption" color="text.secondary" noWrap sx={{ display: 'block' }}>{u.upn}</Typography>}
          <Stack direction="row" spacing={0.75} sx={{ mt: 0.5 }} flexWrap="wrap" useFlexGap>
            <Chip size="small" label={u.role} sx={{ height: 22 }} />
            {u.totp_enabled && <Chip size="small" color="success" label={t('users.security.twofa_on', { defaultValue: '2FA 已启用' })} sx={{ height: 22 }} />}
            {hasSSO && <Chip size="small" variant="outlined" icon={<LinkIcon sx={{ fontSize: 14 }} />} label={u.sso_provider} sx={{ height: 22 }} />}
          </Stack>
        </Box>
        <IconButton onClick={onClose} sx={{ alignSelf: 'flex-start' }}><CloseIcon /></IconButton>
      </Box>

      <Box sx={{ px: 2.5, pb: 3, display: 'flex', flexDirection: 'column', gap: 1.5, overflowY: 'auto' }}>
        <Typography variant="caption" color="text.secondary" sx={{ px: 0.5, mb: 0.5 }}>
          {t('users.security.subtitle', { defaultValue: '管理员可重置 / 吊销 / 重新生成凭据，但无法代用户新增 2FA 或通行密钥（需要用户本人的设备）。' })}
        </Typography>

        <SectionCard md={md} icon={<PasswordIcon fontSize="small" />}
          title={t('users.security.section_password', { defaultValue: '登录密码' })}>
          <Button size="small" variant="outlined" startIcon={<LockResetIcon />} onClick={() => onResetPassword(u)}>
            {t('users.more_menu.reset_password', { defaultValue: '重置密码' })}
          </Button>
        </SectionCard>

        <SectionCard md={md} icon={<ShieldIcon fontSize="small" />}
          title={t('users.security.section_2fa', { defaultValue: '两步验证 (2FA)' })}
          status={u.totp_enabled
            ? <Chip size="small" color="success" label={t('users.security.twofa_on', { defaultValue: '2FA 已启用' })} sx={{ height: 22 }} />
            : <Typography variant="caption" color="text.secondary">{t('users.security.twofa_off', { defaultValue: '未启用' })}</Typography>}>
          <FormControlLabel
            control={<Switch size="small" checked={require2FA} disabled={busy} onChange={(_, c) => toggleRequire2FA(c)} />}
            label={t('users.security.require_2fa', { defaultValue: '要求该用户启用两步验证' })}
            sx={{ ml: 0, mb: 1, display: 'flex', '& .MuiFormControlLabel-label': { ml: 1, fontSize: 14 } }} />
          {u.totp_enabled ? (
            <Stack direction="row" spacing={1} flexWrap="wrap" useFlexGap>
              <Button size="small" variant="outlined" disabled={busy} onClick={doRegenRecovery}>
                {t('users.security.regen_recovery', { defaultValue: '重新生成备用码' })}
              </Button>
              <Button size="small" variant="outlined" color="error" startIcon={<ShieldIcon />} onClick={() => onReset2FA(u)}>
                {t('users.more_menu.reset_2fa', { defaultValue: '重置两步验证' })}
              </Button>
            </Stack>
          ) : (
            <Typography variant="body2" color="text.secondary">
              {t('users.security.twofa_none_hint', { defaultValue: '该用户尚未启用两步验证。' })}
            </Typography>
          )}
          {recoveryCodes && (
            <Alert severity="success" sx={{ mt: 1.5, borderRadius: 2 }}
              action={<IconButton size="small" onClick={() => copy(recoveryCodes.join('\n'))}><ContentCopyIcon fontSize="small" /></IconButton>}>
              <Typography variant="caption" sx={{ fontWeight: 700 }}>
                {t('users.security.regen_recovery_done', { defaultValue: '新备用码（只显示一次，请转交用户）：' })}
              </Typography>
              <Box sx={{ fontFamily: 'monospace', fontSize: 13, mt: 0.5, columns: 2 }}>
                {recoveryCodes.map(c => <div key={c}>{c}</div>)}
              </Box>
            </Alert>
          )}
        </SectionCard>

        <SectionCard md={md} icon={<FingerprintIcon fontSize="small" />}
          title={t('users.security.section_passkeys', { defaultValue: '通行密钥' })}>
          <Button size="small" variant="outlined" startIcon={<FingerprintIcon />} onClick={() => onManagePasskeys(u)}>
            {t('users.more_menu.passkeys', { defaultValue: '管理通行密钥' })}
          </Button>
        </SectionCard>

        <SectionCard md={md} icon={<KeyIcon fontSize="small" />}
          title={t('users.security.section_credentials', { defaultValue: '订阅凭证' })}>
          <Button size="small" variant="outlined" color="error" startIcon={<KeyIcon />} onClick={() => onResetCredentials(u)}>
            {t('users.more_menu.reset_credentials')}
          </Button>
        </SectionCard>

        <SectionCard md={md} icon={<LinkIcon fontSize="small" />}
          title={t('users.security.section_sso', { defaultValue: 'SSO 绑定' })}
          status={<Typography variant="caption" color="text.secondary">
            {hasSSO ? (u.sso_provider || '') : t('users.security.sso_none', { defaultValue: '未绑定' })}
          </Typography>}>
          <Button size="small" variant="outlined" color="error" startIcon={<LinkOffIcon />}
            disabled={!hasSSO} onClick={() => onUnlinkSSO(u)}>
            {t('users.more_menu.unlink_sso')}
          </Button>
        </SectionCard>

        <SectionCard md={md} icon={<EmergencyIcon fontSize="small" />}
          title={t('users.security.section_emergency', { defaultValue: '紧急访问' })}>
          <Button size="small" variant="outlined" startIcon={<EmergencyIcon />} onClick={() => onResetEmergency(u)}>
            {t('users.more_menu.reset_emergency')}
          </Button>
        </SectionCard>
      </Box>
      </>)}
    </Drawer>
  )
}

function SectionCard({ md, icon, title, status, children }: {
  md: M3Tokens
  icon: React.ReactNode
  title: string
  status?: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <Box sx={{ bgcolor: md.surfaceContainer, borderRadius: 3, p: 2 }}>
      <Stack direction="row" alignItems="center" spacing={1.25} sx={{ mb: 1.25 }}>
        <Box sx={{ color: md.onSurfaceVariant, display: 'flex' }}>{icon}</Box>
        <Typography variant="subtitle2" fontWeight={700} sx={{ flex: 1 }}>{title}</Typography>
        {status}
      </Stack>
      {children}
    </Box>
  )
}
