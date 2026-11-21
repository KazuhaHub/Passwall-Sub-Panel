import type { FormEvent } from 'react'
import { useTranslation } from 'react-i18next'

import type { TwoFAMethod } from '@/api/types'
import { IconFingerprint, IconLogin, IconMail } from '@/lib/icons'
import { AsyncButton } from '@/components/common/AsyncButton'
import { Field } from '@/components/form/Field'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'
import { FormAlert } from './parts'

/**
 * The second step of sign-in: the code (or passkey) challenge shown after the password was
 * accepted. It only draws; every decision, request and piece of state lives in LoginView,
 * which passes them in. The methods are equal choices, not "TOTP with fallbacks": the
 * challenge opens on the last-used one and the rest stay behind "use another method".
 */
export interface TwoFactorStepProps {
  mode: TwoFAMethod
  /** The methods the server offers for this challenge, already in display order. */
  methodOrder: TwoFAMethod[]
  code: string
  onCodeChange: (v: string) => void
  error: string
  busy: boolean
  emailSent: boolean
  /** Seconds left before another email code may be requested; 0 when it may. */
  emailCd: number
  showPicker: boolean
  onShowPicker: () => void
  onSubmitCode: (e: FormEvent) => void
  onUsePasskey: () => void
  onSendEmail: () => void
  onSelectMethod: (m: TwoFAMethod) => void
  onBack: () => void
}

export function TwoFactorStep(p: TwoFactorStepProps) {
  const { t } = useTranslation('auth')
  const methodLabel: Record<TwoFAMethod, string> = {
    totp: t('twofa_m_totp'),
    passkey: t('twofa_m_passkey'),
    email: t('twofa_m_email'),
    recovery: t('twofa_m_recovery'),
  }
  // Code-field copy (passkey has no code field, so reuse totp's shape for typing).
  const codeUI = {
    totp: { prompt: t('twofa_prompt'), label: t('twofa_code'), placeholder: '123456' },
    recovery: { prompt: t('twofa_recovery_prompt'), label: t('twofa_recovery_label'), placeholder: 'XXXXX-XXXXX' },
    email: { prompt: t('twofa_email_prompt'), label: t('twofa_email_label'), placeholder: '123456' },
  }[p.mode === 'passkey' ? 'totp' : p.mode]
  const emailWait = t('twofa_email_resend_wait', { sec: p.emailCd, defaultValue: '重新发送（{{sec}}s）' })

  return (
    <div className="flex flex-col gap-4">
      {p.mode === 'passkey' ? (
        <>
          <p className="text-sm text-muted-foreground">{t('twofa_passkey_prompt')}</p>
          {p.error && <FormAlert tone="error">{p.error}</FormAlert>}
          <AsyncButton size="lg" className="w-full" pending={p.busy} icon={<IconFingerprint />} onClick={p.onUsePasskey}>
            {t('twofa_use_passkey')}
          </AsyncButton>
        </>
      ) : p.mode === 'email' && !p.emailSent ? (
        // Last-used was email but no code is out yet (we never auto-send on load,
        // to avoid mailing on every challenge): offer to send one.
        <>
          <p className="text-sm text-muted-foreground">{t('twofa_email_prompt')}</p>
          {p.error && <FormAlert tone="error">{p.error}</FormAlert>}
          <AsyncButton size="lg" className="w-full" pending={p.busy} disabled={p.emailCd > 0} icon={<IconMail />} onClick={p.onSendEmail}>
            {p.emailCd > 0 ? emailWait : t('twofa_email_send', { defaultValue: '发送验证码' })}
          </AsyncButton>
        </>
      ) : (
        <form onSubmit={p.onSubmitCode} noValidate className="flex flex-col gap-4">
          <p className="text-sm text-muted-foreground">{codeUI.prompt}</p>
          <Field label={codeUI.label} error={p.error || undefined} errorBlocksSave={false}>
            <Input name="otp" value={p.code} onChange={e => p.onCodeChange(e.target.value)} autoFocus
              autoComplete="one-time-code" placeholder={codeUI.placeholder} inputMode="text" autoCapitalize="characters" />
          </Field>
          <AsyncButton type="submit" size="lg" className="w-full" pending={p.busy} icon={<IconLogin />}>
            {t('twofa_submit')}
          </AsyncButton>
          {p.mode === 'email' && (
            <Button type="button" variant="ghost" size="sm" className="w-full" disabled={p.busy || p.emailCd > 0} onClick={p.onSendEmail}>
              <IconMail />
              {p.emailCd > 0 ? emailWait : t('twofa_email_resend', { defaultValue: '重新发送验证码' })}
            </Button>
          )}
        </form>
      )}

      {/* Method switcher: collapsed to "use another method", expands to the full chooser.
          Only when more than one method is offered. */}
      {p.methodOrder.length > 1 && (
        p.showPicker ? (
          <div className="flex flex-col gap-1.5">
            <p className="text-xs text-muted-foreground">{t('twofa_choose_method')}</p>
            <div className="flex flex-wrap gap-1.5">
              {p.methodOrder.map(m => (
                <Button key={m} type="button" size="sm" aria-pressed={p.mode === m}
                  variant={p.mode === m ? 'secondary' : 'outline'}
                  className={cn(p.mode === m && 'ring-2 ring-brand')}
                  disabled={p.busy} onClick={() => p.onSelectMethod(m)}>
                  {m === 'passkey' ? <IconFingerprint /> : m === 'email' ? <IconMail /> : null}
                  {methodLabel[m]}
                </Button>
              ))}
            </div>
          </div>
        ) : (
          <Button type="button" variant="ghost" size="sm" className="w-full" disabled={p.busy} onClick={p.onShowPicker}>
            {t('twofa_use_other', { defaultValue: '使用其他方式验证' })}
          </Button>
        )
      )}

      <Button type="button" variant="ghost" size="sm" className="w-full" disabled={p.busy} onClick={p.onBack}>
        {t('twofa_back')}
      </Button>
    </div>
  )
}
