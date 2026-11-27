import { useState } from 'react'
import type { User } from '@/api/types'
import { regenerateUser2FARecovery } from '@/api/users'
import { AsyncButton } from '@/components/common/AsyncButton'
import { CopyButton } from '@/components/common/CopyButton'
import { StatusBadge } from '@/components/common/StatusBadge'
import { confirm } from '@/components/ConfirmHost'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Block } from './blocks'
import { useUsers } from './context'
import { useAdminT } from './useAdminT'

/**
 * Every account-security action for one user, in one place. An admin cannot ENROLL 2FA or a passkey for someone
 * (that needs the user's own device), only reset, revoke or regenerate. Each action names the account and asks
 * first, with focus on Cancel, because each one locks something out or hands someone a new secret.
 */
export function SecurityTab({ user }: { user: User }) {
  const t = useAdminT()
  const { act } = useUsers()
  const [codes, setCodes] = useState<string[] | null>(null)
  const hasSSO = !!user.sso_provider && user.sso_provider !== 'local'
  // A passkey is a second factor, so "has 2FA" (and with it recovery codes) is an authenticator app OR at least one passkey.
  const has2FA = !!(user.totp_enabled || (user.passkey_count ?? 0) > 0)

  async function regenerate() {
    const ok = await confirm({
      title: t('users.security.regen_recovery_title'),
      message: t('users.security.regen_recovery_message', { upn: user.upn }),
      confirmText: t('users.security.regen_recovery_confirm'),
      destructive: true,
      initialFocus: 'cancel',
    })
    if (!ok) return
    try { setCodes(await regenerateUser2FARecovery(user.id)) } catch { /* reported by the request layer */ }
  }

  return (
    <div className="flex flex-col gap-4">
      <p className="text-sm text-muted-foreground">{t('users.security.subtitle')}</p>

      <Block title={t('users.security.section_password')}
        actions={<Button size="sm" variant="outline" onClick={() => act.resetPassword(user)}>{t('users.more_menu.reset_password')}</Button>} />

      <Block title={t('users.security.section_2fa')}
        actions={has2FA ? <StatusBadge tone="success" label={t('users.security.twofa_on')} /> : <span className="text-xs text-muted-foreground">{t('users.security.twofa_off')}</span>}>
        {has2FA ? (
          <div className="flex flex-wrap gap-2">
            {/* Recovery codes are decoupled from the authenticator app: a passkey-only account has them too. */}
            <AsyncButton size="sm" variant="outline" onClick={regenerate}>{t('users.security.regen_recovery')}</AsyncButton>
            {user.totp_enabled && <AsyncButton size="sm" variant="outline" className="text-destructive" onClick={() => act.reset2fa(user)}>{t('users.more_menu.reset_2fa')}</AsyncButton>}
          </div>
        ) : <p className="text-sm text-muted-foreground">{t('users.security.twofa_none_hint')}</p>}
        {codes && (
          <Alert>
            <AlertDescription className="flex flex-col gap-2">
              <span className="font-medium text-foreground">{t('users.security.regen_recovery_done')}</span>
              <span className="grid grid-cols-2 gap-x-4 font-mono text-[0.8125rem]">{codes.map(c => <span key={c}>{c}</span>)}</span>
              <span><CopyButton value={codes.join('\n')} name={t('users.security.regen_recovery')} size="icon-sm" /></span>
            </AlertDescription>
          </Alert>
        )}
      </Block>

      <Block title={t('users.security.section_passkeys')} description={t('users.security.passkey_count', { count: user.passkey_count ?? 0 })}
        actions={<Button size="sm" variant="outline" onClick={() => act.managePasskeys(user)}>{t('users.more_menu.passkeys')}</Button>} />

      <Block title={t('users.security.section_credentials')}
        actions={<AsyncButton size="sm" variant="outline" className="text-destructive" onClick={() => act.resetCredentials(user)}>{t('users.more_menu.reset_credentials')}</AsyncButton>} />

      <Block title={t('users.security.section_sso')} description={hasSSO ? user.sso_provider : t('users.security.sso_none')}
        actions={<AsyncButton size="sm" variant="outline" className="text-destructive" disabled={!hasSSO} onClick={() => act.unlinkSso(user)}>{t('users.more_menu.unlink_sso')}</AsyncButton>} />

      <Block title={t('users.security.section_emergency')}
        actions={<AsyncButton size="sm" variant="outline" onClick={() => act.resetEmergency(user)}>{t('users.more_menu.reset_emergency')}</AsyncButton>} />
    </div>
  )
}
