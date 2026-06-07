import { useEffect, useState } from 'react'
import {
  Box, Button, Card, CircularProgress, Divider, Link as MuiLink, Stack, Typography, useTheme,
} from '@mui/material'
import ShieldIcon from '@mui/icons-material/GppGood'
import FingerprintIcon from '@mui/icons-material/Fingerprint'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'

import { getMyProfile, type MeProfile } from '@/api/me'
import { useAuthStore } from '@/stores/auth'
import { useSiteStore } from '@/stores/site'
import { homeForRole } from '@/router/home'
import BrandLogo from '@/components/BrandLogo'
import LanguageMenu from '@/components/LanguageMenu'
import AppearanceMenu from '@/components/AppearanceMenu'
import { useAppearanceStore } from '@/stores/appearance'
import { setLanguage, currentLanguage } from '@/i18n'
import type { AppLanguage } from '@/theme'
import TwoFactorDialog from '@/views/user/TwoFactorDialog'
import PasskeyDialog from '@/views/user/PasskeyDialog'

// Enroll2FAView is the mandatory-2FA gate: an account that an admin requires to
// have a second factor (per-user / group / staff-wide) lands here until it
// enrolls one. The backend hard-gate 403s every other authenticated route with
// code 2fa_enrollment_required, and the axios interceptor bounces here. It reuses
// the normal TOTP / passkey enrollment dialogs; once a factor exists, it routes
// the user on to the panel.
export default function Enroll2FAView() {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation(['auth', 'user'])
  const navigate = useNavigate()
  const auth = useAuthStore()
  const site = useSiteStore()
  const appearance = useAppearanceStore()

  const [profile, setProfile] = useState<MeProfile | null>(null)
  const [loading, setLoading] = useState(true)
  const [totpOpen, setTotpOpen] = useState(false)
  const [passkeyOpen, setPasskeyOpen] = useState(false)

  useEffect(() => { void site.load() }, [site])

  async function refresh() {
    try {
      const p = await getMyProfile()
      setProfile(p)
      // Requirement satisfied (enrolled, or admin lifted it) → on to the panel.
      if (!p.must_enroll_2fa) {
        navigate(homeForRole(auth.role || 'user'), { replace: true })
      }
    } catch { /* interceptor handles auth errors */ }
    finally { setLoading(false) }
  }

  useEffect(() => { void refresh() }, []) // eslint-disable-line react-hooks/exhaustive-deps

  function logout() {
    auth.logout()
    navigate('/login', { replace: true })
  }

  return (
    <Box sx={{ position: 'fixed', inset: 0, display: 'flex', flexDirection: 'column', bgcolor: md.surface }}>
      <Box sx={{ display: 'flex', justifyContent: 'flex-end', p: 1.5, gap: 0.5 }}>
        <LanguageMenu value={currentLanguage()} onChange={(l: AppLanguage) => setLanguage(l)} />
        <AppearanceMenu
          state={{ systemColor: appearance.systemColor, userColor: appearance.userColor, mode: appearance.mode }}
          onChange={(patch) => {
            if ('userColor' in patch) appearance.setUserColor(patch.userColor ?? null)
            if (patch.mode) appearance.setMode(patch.mode)
          }}
        />
      </Box>
      <Box sx={{ flex: 1, display: 'grid', placeItems: 'center', px: 2 }}>
        <Card sx={{ width: '100%', maxWidth: 440, bgcolor: md.surfaceContainerLow, p: 4 }}>
          <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'center', mb: 3 }}>
            <BrandLogo height={48} />
            <Typography variant="h5" sx={{ fontWeight: 500, mt: 1.5 }}>
              {t('auth:enroll2fa_title', { defaultValue: '需要设置两步验证' })}
            </Typography>
            <Typography variant="body2" sx={{ mt: 0.5, color: md.onSurfaceVariant, textAlign: 'center' }}>
              {t('auth:enroll2fa_subtitle', { defaultValue: '管理员要求你的账号必须启用两步验证后才能继续使用面板。' })}
            </Typography>
          </Box>

          {loading ? (
            <Box sx={{ display: 'grid', placeItems: 'center', py: 3 }}><CircularProgress /></Box>
          ) : (
            <Stack spacing={1.5}>
              <Button variant="contained" size="large" fullWidth startIcon={<ShieldIcon />}
                disabled={!profile?.totp_available} onClick={() => setTotpOpen(true)}>
                {t('auth:enroll2fa_totp', { defaultValue: '设置身份验证器 (TOTP)' })}
              </Button>
              {profile?.passkey_available && (
                <>
                  <Divider sx={{ color: md.onSurfaceVariant, fontSize: 12 }}>{t('auth:or', { defaultValue: '或' })}</Divider>
                  <Button variant="outlined" size="large" fullWidth startIcon={<FingerprintIcon />}
                    onClick={() => setPasskeyOpen(true)}>
                    {t('auth:enroll2fa_passkey', { defaultValue: '添加通行密钥' })}
                  </Button>
                </>
              )}
            </Stack>
          )}

          <Box sx={{ mt: 3, textAlign: 'center' }}>
            <MuiLink component="button" type="button" variant="body2" onClick={logout}>
              {t('auth:enroll2fa_logout', { defaultValue: '退出登录' })}
            </MuiLink>
          </Box>
        </Card>
      </Box>

      <TwoFactorDialog open={totpOpen} enabled={false} hasPasskey={false} md={md}
        onClose={() => setTotpOpen(false)} onChanged={() => { setTotpOpen(false); void refresh() }} />
      <PasskeyDialog open={passkeyOpen} available={!!profile?.passkey_available}
        credentials={profile?.passkey_credentials ?? []} md={md}
        onClose={() => setPasskeyOpen(false)} onChanged={() => { setPasskeyOpen(false); void refresh() }} />
    </Box>
  )
}
