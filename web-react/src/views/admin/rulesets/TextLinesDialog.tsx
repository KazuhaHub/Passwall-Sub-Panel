import { lazy, Suspense, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { FormDialog } from '@/components/data/FormDialog'
import { IconSpinner } from '@/lib/icons'

const CodeEditor = lazy(() => import('@/components/CodeEditor'))

/**
 * A few lines of the rule set as text, for what the form cannot edit (a logical rule, a sub-rule jump, a line the parser
 * does not read). What is entered replaces those lines and is read again, so it may become several rules or turn into
 * one the form can edit.
 */
export function TextLinesDialog({ title, initial, onSubmit, onClose }: {
  title: string
  initial: string
  onSubmit: (text: string) => void
  onClose: () => void
}) {
  const { t } = useTranslation(['admin', 'common'])
  const [text, setText] = useState(initial)
  return (
    <FormDialog title={title} size="lg" dirty={text !== initial} pending={false} submitLabel={t('common:actions.save')} onClose={onClose} onSubmit={() => onSubmit(text)}>
      <div className="overflow-hidden rounded-lg border">
        <Suspense fallback={<div className="grid h-40 place-items-center"><IconSpinner aria-hidden className="animate-spin text-muted-foreground" /></div>}>
          <CodeEditor value={text} onChange={setText} minRows={4} ariaLabel={title} />
        </Suspense>
      </div>
      <p className="text-xs text-muted-foreground">{t('admin:rules.editor_keyboard_hint')}</p>
    </FormDialog>
  )
}
