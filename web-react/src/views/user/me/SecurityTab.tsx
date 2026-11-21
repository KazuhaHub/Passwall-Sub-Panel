import type { ReactNode } from 'react'
import type { MeProfile } from '@/api/me'
import { StatusDot } from '@/components/common/StatusDot'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { useMeT } from './useMeT'

type SecurityProfile = Pick<MeProfile, 'can_change_password' | 'totp_available' | 'totp_enabled' | 'passkey_available' | 'passkey_enabled' | 'passkey_credentials' | 'recovery_codes_remaining'>

/**
 * Which security rows this account gets. Each is the condition the old menu used for the same entry, so nothing appears
 * for an account that could not use it (an SSO-only account has no local password to change or to add a second factor to).
 * Recovery codes follow ANY second factor, TOTP or a passkey, because they are decoupled from TOTP.
 */
export function securityRows(p: SecurityProfile) {
  const passkeys = p.passkey_credentials?.length ?? 0
  return {
    password: !!p.can_change_password,
    totp: !!(p.totp_available || p.totp_enabled),
    passkeys: !!(p.passkey_available || p.passkey_enabled),
    recovery: !!(p.totp_enabled || passkeys > 0),
  }
}

export const hasSecurityRows = (p: SecurityProfile) => Object.values(securityRows(p)).some(Boolean)

function Row({ title, status, action }: { title: string; status?: ReactNode; action: ReactNode }) {
  return (
    <li className="flex items-center justify-between gap-3 py-3 first:pt-0 last:pb-0">
      <div className="min-w-0">
        <div className="font-medium">{title}</div>
        {status && <div className="text-sm text-muted-foreground">{status}</div>}
      </div>
      <div className="shrink-0">{action}</div>
    </li>
  )
}

/**
 * Account security: password, authenticator app, passkeys, recovery codes. Each row says its state in words and has one
 * button that opens the dialog doing the work. This is also where a person who is required to add a second factor
 * completes it: the ceremonies are the same dialogs the enrolment page opens.
 */
export function SecurityTab({ profile, onPassword, onTotp, onPasskeys, onRecovery }: {
  profile: SecurityProfile
  onPassword: () => void
  onTotp: () => void
  onPasskeys: () => void
  onRecovery: () => void
}) {
  const { t } = useMeT()
  const rows = securityRows(profile)
  return (
    <Card>
      <CardHeader><CardTitle>{t('security.title')}</CardTitle></CardHeader>
      <CardContent>
        <ul className="divide-y">
          {rows.password && <Row title={t('actions.change_password')} action={<Button variant="outline" size="sm" onClick={onPassword}>{t('security.change')}</Button>} />}
          {rows.totp && (
            <Row
              title={t('actions.two_factor')}
              status={<StatusDot tone={profile.totp_enabled ? 'success' : 'neutral'} label={profile.totp_enabled ? t('twofa.status_on') : t('twofa.status_off')} />}
              action={<Button variant="outline" size="sm" onClick={onTotp}>{profile.totp_enabled ? t('security.manage') : t('security.set_up')}</Button>}
            />
          )}
          {rows.passkeys && (
            <Row
              title={t('actions.passkeys')}
              status={t('passkey.count', { count: profile.passkey_credentials?.length ?? 0 })}
              action={<Button variant="outline" size="sm" onClick={onPasskeys}>{t('security.manage')}</Button>}
            />
          )}
          {rows.recovery && (
            <Row
              title={t('actions.recovery_codes')}
              status={t('recovery.remaining', { count: profile.recovery_codes_remaining ?? 0 })}
              action={<Button variant="outline" size="sm" onClick={onRecovery}>{t('security.manage')}</Button>}
            />
          )}
        </ul>
      </CardContent>
    </Card>
  )
}
