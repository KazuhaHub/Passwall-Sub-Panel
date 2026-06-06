import { useState } from 'react'
import {
  Alert,
  Box,
  Button,
  Chip,
  Divider,
  Drawer,
  IconButton,
  Stack,
  Typography,
} from '@mui/material'
import CloseIcon from '@mui/icons-material/Close'
import ContentCopyIcon from '@mui/icons-material/ContentCopy'
import LockResetIcon from '@mui/icons-material/LockReset'
import ShieldIcon from '@mui/icons-material/GppGood'
import FingerprintIcon from '@mui/icons-material/Fingerprint'
import KeyIcon from '@mui/icons-material/VpnKey'
import LinkOffIcon from '@mui/icons-material/LinkOff'
import EmergencyIcon from '@mui/icons-material/MedicalServices'
import { useTranslation } from 'react-i18next'

import { regenerateUser2FARecovery } from '@/api/users'
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

  // Clear the one-time recovery panel whenever the target user changes.
  const targetId = user?.id
  const [shownFor, setShownFor] = useState<number | undefined>(undefined)
  if (open && targetId !== shownFor) {
    setShownFor(targetId)
    setRecoveryCodes(null)
  }

  if (!user) return null
  const u = user
  const hasSSO = !!u.sso_provider && u.sso_provider !== 'local'

  async function copy(text: string) {
    try {
      await navigator.clipboard.writeText(text)
      pushSnack(t('users.toast.copied'), 'success')
    } catch { /* ignore */ }
  }

  async function doRegenRecovery() {
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
      PaperProps={{ sx: { width: 460, maxWidth: '92vw', bgcolor: md.surfaceContainerLow, p: 0 } }}>
      <Box sx={{ p: 2.5, display: 'flex', alignItems: 'center', gap: 1.5, borderBottom: `1px solid ${md.outlineVariant}` }}>
        <Box sx={{ flex: 1, minWidth: 0 }}>
          <Typography variant="subtitle1" fontWeight={700} noWrap>{u.upn}</Typography>
          <Stack direction="row" spacing={0.75} sx={{ mt: 0.5 }} flexWrap="wrap" useFlexGap>
            <Chip size="small" label={u.role} />
            {u.totp_enabled && <Chip size="small" color="success" label={t('users.security.twofa_on', { defaultValue: '2FA 已启用' })} />}
            {hasSSO && <Chip size="small" variant="outlined" label={u.sso_provider} />}
          </Stack>
        </Box>
        <IconButton onClick={onClose}><CloseIcon /></IconButton>
      </Box>

      <Box sx={{ p: 2.5, display: 'flex', flexDirection: 'column', gap: 2.5, overflowY: 'auto' }}>
        <Typography variant="caption" color="text.secondary">
          {t('users.security.subtitle', { defaultValue: '管理员可重置 / 吊销 / 重新生成凭据，但无法代用户新增 2FA 或通行密钥（需要用户本人的设备）。' })}
        </Typography>

        <Section title={t('users.security.section_password', { defaultValue: '登录密码' })}>
          <Button size="small" variant="outlined" startIcon={<LockResetIcon />} onClick={() => onResetPassword(u)}>
            {t('users.more_menu.reset_password', { defaultValue: '重置密码' })}
          </Button>
        </Section>

        <Divider />

        <Section title={t('users.security.section_2fa', { defaultValue: '两步验证 (2FA)' })}
          status={u.totp_enabled
            ? t('users.security.twofa_on', { defaultValue: '2FA 已启用' })
            : t('users.security.twofa_off', { defaultValue: '未启用' })}>
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
            <Alert severity="success" sx={{ mt: 1.5 }}
              action={<IconButton size="small" onClick={() => copy(recoveryCodes.join('\n'))}><ContentCopyIcon fontSize="small" /></IconButton>}>
              <Typography variant="caption" sx={{ fontWeight: 700 }}>
                {t('users.security.regen_recovery_done', { defaultValue: '新备用码（只显示一次，请转交用户）：' })}
              </Typography>
              <Box sx={{ fontFamily: 'monospace', fontSize: 13, mt: 0.5, columns: 2 }}>
                {recoveryCodes.map(c => <div key={c}>{c}</div>)}
              </Box>
            </Alert>
          )}
        </Section>

        <Divider />

        <Section title={t('users.security.section_passkeys', { defaultValue: '通行密钥' })}>
          <Button size="small" variant="outlined" startIcon={<FingerprintIcon />} onClick={() => onManagePasskeys(u)}>
            {t('users.more_menu.passkeys', { defaultValue: '管理通行密钥' })}
          </Button>
        </Section>

        <Divider />

        <Section title={t('users.security.section_credentials', { defaultValue: '订阅凭证' })}>
          <Button size="small" variant="outlined" color="error" startIcon={<KeyIcon />} onClick={() => onResetCredentials(u)}>
            {t('users.more_menu.reset_credentials')}
          </Button>
        </Section>

        <Divider />

        <Section title={t('users.security.section_sso', { defaultValue: 'SSO 绑定' })}
          status={hasSSO ? (u.sso_provider || '') : t('users.security.sso_none', { defaultValue: '未绑定' })}>
          <Button size="small" variant="outlined" color="error" startIcon={<LinkOffIcon />}
            disabled={!hasSSO} onClick={() => onUnlinkSSO(u)}>
            {t('users.more_menu.unlink_sso')}
          </Button>
        </Section>

        <Divider />

        <Section title={t('users.security.section_emergency', { defaultValue: '紧急访问' })}>
          <Button size="small" variant="outlined" startIcon={<EmergencyIcon />} onClick={() => onResetEmergency(u)}>
            {t('users.more_menu.reset_emergency')}
          </Button>
        </Section>
      </Box>
    </Drawer>
  )
}

function Section({ title, status, children }: { title: string; status?: string; children: React.ReactNode }) {
  return (
    <Box>
      <Stack direction="row" alignItems="center" justifyContent="space-between" sx={{ mb: 1 }}>
        <Typography variant="subtitle2" fontWeight={700}>{title}</Typography>
        {status && <Typography variant="caption" color="text.secondary">{status}</Typography>}
      </Stack>
      {children}
    </Box>
  )
}
