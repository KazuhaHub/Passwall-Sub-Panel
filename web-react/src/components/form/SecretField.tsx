import { useState, type ComponentProps } from 'react'
import { EyeIcon, EyeOffIcon } from 'lucide-react'
import { useSpacedT } from '@/i18n/spacing'
import { InputGroup, InputGroupAddon, InputGroupInput } from '@/components/ui/input-group'
import { CopyButton } from '@/components/common/CopyButton'
import { IconButton } from '@/components/common/IconButton'

export interface SecretFieldProps extends Omit<ComponentProps<'input'>, 'value' | 'onChange' | 'type'> {
  value: string
  onChange?: (next: string) => void
  /**
   * A secret is already stored server-side and its value is never sent back.
   * The empty box then says so, and leaving it empty means "keep it" (the
   * convention every encrypted-at-rest setting here follows).
   */
  isSet?: boolean
}

/** Masked by default; reveal and copy are explicit, and both have tooltips and labels. */
export function SecretField({ value, onChange, isSet, readOnly, placeholder, ...rest }: SecretFieldProps) {
  const { t } = useSpacedT()
  const [shown, setShown] = useState(false)
  return (
    <InputGroup>
      <InputGroupInput
        {...rest}
        type={shown ? 'text' : 'password'}
        value={value}
        readOnly={readOnly}
        onChange={e => onChange?.(e.target.value)}
        placeholder={isSet && !value ? t('ui.form.secret_is_set') : placeholder}
        // autocomplete="off" is ignored by Chrome on a password input, which then offers
        // to save an API token as a login. "new-password" is honoured; the data-* flags
        // are the opt-outs 1Password, LastPass and Bitwarden check for.
        autoComplete="new-password"
        data-1p-ignore=""
        data-lpignore="true"
        data-bwignore="true"
        spellCheck={false}
        className="font-mono"
      />
      <InputGroupAddon align="inline-end">
        <IconButton label={shown ? t('ui.form.secret_hide') : t('ui.form.secret_show')} size="icon-xs" side="top" onClick={() => setShown(s => !s)}>
          {shown ? <EyeOffIcon /> : <EyeIcon />}
        </IconButton>
        {value && <CopyButton value={value} size="icon-xs" side="top" />}
      </InputGroupAddon>
    </InputGroup>
  )
}
