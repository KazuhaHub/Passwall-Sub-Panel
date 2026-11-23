import { Field } from '@/components/form/Field'
import { Textarea } from '@/components/ui/textarea'
import { useAdminT } from '../../useAdminT'

/**
 * A secret that is several lines (a PEM private key). SecretField is one line, and a password box strips the newlines
 * from what is pasted into it, which would corrupt a key, so this is a text area whose text is drawn as dots. It is the
 * same contract: the stored value is never shown or sent back, and leaving it empty keeps the stored one.
 */
export function SecretTextarea({ id, label, isSet, value, onChange }: { id: string; label: string; isSet: boolean; value: string; onChange: (next: string) => void }) {
  const t = useAdminT()
  return (
    <Field label={label} hint={isSet ? t('settings.secret_hint') : undefined}>
      <Textarea id={id} rows={4} spellCheck={false} autoComplete="off" data-1p-ignore="" data-lpignore="true" data-bwignore="true"
        className="font-mono text-xs [-webkit-text-security:disc]" value={value} placeholder={isSet && !value ? t('common:ui.form.secret_is_set') : undefined}
        onChange={e => onChange(e.target.value)} />
    </Field>
  )
}
