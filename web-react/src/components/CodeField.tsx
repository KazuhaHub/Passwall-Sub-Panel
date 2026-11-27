import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Field } from '@/components/form/Field'
import { LazyCodeEditor } from '@/components/LazyCodeEditor'
import type { CodeEditorProps } from '@/components/CodeEditor'

/**
 * A labelled field around the code editor: what a text area with a Field around it was, for source text.
 * Field wires `id`, `aria-describedby`, `aria-invalid` and `aria-required` onto the editor as it does onto
 * an input; the editor finds the label that points at its id for its accessible name.
 */
export function CodeField({ label, hint, error, errorBlocksSave, required, fieldClassName, ...editor }: Omit<CodeEditorProps, 'aria-describedby' | 'aria-invalid' | 'aria-required'> & {
  label: ReactNode
  hint?: ReactNode
  error?: ReactNode
  errorBlocksSave?: boolean
  required?: boolean
  /** A className for the Field (the wrapper); `className` goes to the editor. */
  fieldClassName?: string
  /** Sizes the placeholder shown while the editor loads. */
  fallbackHeight?: string
}) {
  const { t } = useTranslation()
  // Where Tab indents, the way out of the editor is told under it (admin:rules.editor_keyboard_hint), as on the rule set page.
  const withKeys = !editor.tabMovesFocus && !editor.readOnly
  const shownHint = withKeys ? <>{hint}{hint ? ' ' : ''}{t('admin:rules.editor_keyboard_hint')}</> : hint
  return (
    <Field label={label} hint={shownHint} error={error} errorBlocksSave={errorBlocksSave} required={required} className={fieldClassName}>
      <LazyCodeEditor {...editor} />
    </Field>
  )
}
