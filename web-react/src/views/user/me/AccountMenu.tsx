import type { MeProfile } from '@/api/me'
import { IconButton } from '@/components/common/IconButton'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { IconFingerprint, IconLock, IconMoreVertical, IconShield, IconTicket } from '@/lib/icons'
import { useMeT } from './useMeT'

type SecurityProfile = Pick<MeProfile, 'can_change_password' | 'totp_available' | 'totp_enabled' | 'passkey_available' | 'passkey_enabled' | 'passkey_credentials' | 'recovery_codes_remaining'>

/**
 * Which security entries this account gets. Each is the condition the portal's menu has always used for the same entry,
 * so nothing appears for an account that could not use it (an SSO-only account has no local password to change or to add
 * a second factor to). Recovery codes follow ANY second factor, TOTP or a passkey, because they are decoupled from TOTP.
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

function Entry({ icon, title, status, onSelect }: { icon: React.ReactNode; title: string; status?: string; onSelect: () => void }) {
  return (
    <DropdownMenuItem onClick={onSelect} className="items-start gap-3 py-2">
      <span className="mt-0.5 [&_svg]:size-4">{icon}</span>
      <span className="flex min-w-0 flex-col">
        <span>{title}</span>
        {status && <span className="text-xs font-normal text-muted-foreground">{status}</span>}
      </span>
    </DropdownMenuItem>
  )
}

/**
 * The account's security actions, behind the ⋮ beside the name: change password, authenticator app, passkeys, recovery
 * codes. Each entry says its state under its name and opens the dialog that does the work; those are also the dialogs a
 * person who is required to add a second factor completes it in. With nothing to offer the button is not drawn at all.
 */
export function AccountMenu({ profile, onPassword, onTotp, onPasskeys, onRecovery }: {
  profile: SecurityProfile
  onPassword: () => void
  onTotp: () => void
  onPasskeys: () => void
  onRecovery: () => void
}) {
  const { t } = useMeT()
  const rows = securityRows(profile)
  if (!hasSecurityRows(profile)) return null
  return (
    <DropdownMenu>
      <DropdownMenuTrigger render={<IconButton label={t('security.title')} />}><IconMoreVertical /></DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-64">
        {rows.password && <Entry icon={<IconLock />} title={t('actions.change_password')} onSelect={onPassword} />}
        {rows.totp && (
          <Entry icon={<IconShield />} title={t('actions.two_factor')} onSelect={onTotp}
            status={profile.totp_enabled ? t('twofa.status_on') : t('twofa.status_off')} />
        )}
        {rows.passkeys && (
          <Entry icon={<IconFingerprint />} title={t('actions.passkeys')} onSelect={onPasskeys}
            status={t('passkey.count', { count: profile.passkey_credentials?.length ?? 0 })} />
        )}
        {rows.recovery && (
          <Entry icon={<IconTicket />} title={t('actions.recovery_codes')} onSelect={onRecovery}
            status={t('recovery.remaining', { count: profile.recovery_codes_remaining ?? 0 })} />
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
