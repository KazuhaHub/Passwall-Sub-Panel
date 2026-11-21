import { useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router'
import { useTranslation } from 'react-i18next'

import { getMyProfile, type MeProfile } from '@/api/me'
import { IconFingerprint, IconShield } from '@/lib/icons'
import { Button } from '@/components/ui/button'
import { CardSkeleton } from '@/components/feedback/LoadingState'
import { useAuthStore } from '@/stores/auth'
import { selectEffectiveColor, resolveEffectiveMode, useAppearanceStore } from '@/stores/appearance'
import { homeForRole } from '@/router/home'
import { tokensFromSource } from '@/theme/tokensFromSource'
import TwoFactorDialog from '@/views/user/TwoFactorDialog'
import PasskeyDialog from '@/views/user/PasskeyDialog'
import { AuthLayout } from '@/views/auth/AuthLayout'
import { OrDivider } from '@/views/auth/parts'

// The two enrolment dialogs live under views/user and are still drawn with the M3 colour
// tokens passed in as a prop (the portal pages have not moved yet). The tokens are computed
// from the appearance store directly, without a theme provider; when those dialogs move to
// the new components this goes away with the prop.
function useM3Tokens() {
  const mode = useAppearanceStore(s => s.mode)
  const color = useAppearanceStore(selectEffectiveColor)
  return useMemo(() => {
    const systemDark = typeof window !== 'undefined' && !!window.matchMedia?.('(prefers-color-scheme: dark)').matches
    return tokensFromSource(color, resolveEffectiveMode(mode, systemDark))
  }, [mode, color])
}

// Enroll2FAView is the mandatory-2FA gate: an account that an admin requires to
// have a second factor (per-user / group / staff-wide) lands here until it
// enrolls one. The backend hard-gate 403s every other authenticated route with
// code 2fa_enrollment_required, and the axios interceptor bounces here. It reuses
// the normal TOTP / passkey enrollment dialogs; once a factor exists, it routes
// the user on to the panel.
export default function Enroll2FAView() {
  const { t } = useTranslation(['auth', 'user'])
  const navigate = useNavigate()
  const auth = useAuthStore()
  const md = useM3Tokens()

  const [profile, setProfile] = useState<MeProfile | null>(null)
  const [loading, setLoading] = useState(true)
  const [totpOpen, setTotpOpen] = useState(false)
  const [passkeyOpen, setPasskeyOpen] = useState(false)

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
    <AuthLayout wide
      title={t('auth:enroll2fa_title', { defaultValue: '需要设置两步验证' })}
      subtitle={t('auth:enroll2fa_subtitle', { defaultValue: '管理员要求你的账号必须启用两步验证后才能继续使用面板。' })}>
      {loading ? (
        <CardSkeleton lines={2} />
      ) : (
        <div className="flex flex-col gap-3">
          <Button size="lg" className="w-full" disabled={!profile?.totp_available} onClick={() => setTotpOpen(true)}>
            <IconShield />
            {t('auth:enroll2fa_totp', { defaultValue: '设置身份验证器 (TOTP)' })}
          </Button>
          {profile?.passkey_available && (
            <>
              <OrDivider />
              <Button variant="outline" size="lg" className="w-full" onClick={() => setPasskeyOpen(true)}>
                <IconFingerprint />
                {t('auth:enroll2fa_passkey', { defaultValue: '添加通行密钥' })}
              </Button>
            </>
          )}
        </div>
      )}

      <div className="text-center">
        <Button variant="ghost" size="sm" onClick={logout}>
          {t('auth:enroll2fa_logout', { defaultValue: '退出登录' })}
        </Button>
      </div>

      <TwoFactorDialog open={totpOpen} enabled={false} hasPasskey={false} md={md}
        onClose={() => setTotpOpen(false)} onChanged={() => { setTotpOpen(false); void refresh() }} />
      <PasskeyDialog open={passkeyOpen} available={!!profile?.passkey_available}
        credentials={profile?.passkey_credentials ?? []} md={md}
        onClose={() => setPasskeyOpen(false)} onChanged={() => { setPasskeyOpen(false); void refresh() }} />
    </AuthLayout>
  )
}
