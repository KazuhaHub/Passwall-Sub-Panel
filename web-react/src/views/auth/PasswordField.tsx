import { useState, type ComponentProps } from 'react'
import { useTranslation } from 'react-i18next'

import { IconHide, IconShow } from '@/lib/icons'
import { IconButton } from '@/components/common/IconButton'
import { InputGroup, InputGroupAddon, InputGroupInput } from '@/components/ui/input-group'

/**
 * A password box with a show/hide toggle. It is NOT SecretField: that one forces
 * `autocomplete="new-password"` and opts out of password managers, which is right for an
 * API token and wrong here. The caller sets the autocomplete token (`current-password`
 * to sign in, `new-password` to choose one) and the box is always `type=password` unless
 * the person asks to see it. `id` and the aria props arrive from `Field` by cloning.
 */
export function PasswordField({ autoComplete, ...rest }: Omit<ComponentProps<'input'>, 'type'> & {
  autoComplete: 'current-password' | 'new-password'
}) {
  const { t } = useTranslation('common')
  const [shown, setShown] = useState(false)
  return (
    <InputGroup>
      <InputGroupInput {...rest} type={shown ? 'text' : 'password'} autoComplete={autoComplete} />
      <InputGroupAddon align="inline-end">
        <IconButton
          label={shown ? t('ui.form.secret_hide') : t('ui.form.secret_show')}
          size="icon-xs"
          side="top"
          aria-pressed={shown}
          onClick={() => setShown(s => !s)}
        >
          {shown ? <IconHide /> : <IconShow />}
        </IconButton>
      </InputGroupAddon>
    </InputGroup>
  )
}
