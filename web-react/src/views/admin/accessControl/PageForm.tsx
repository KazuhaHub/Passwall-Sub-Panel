import type { ReactNode } from 'react'
import { useFormValidity } from '@/components/form/FormValidity'

/**
 * The form element of an editor page. A form with several text boxes and no submit button is not submitted by Enter
 * in the browser, so Enter in a plain single-line box (not a picker, a code editor or a button) is sent to submit
 * here. Enter in a text box saves, with the same two refusals the Save button has: while
 * the form holds a problem (the SaveBar names it) and while a save is already running, so a second Enter or a click
 * beside it never starts a second write. Must sit inside the FormValidityProvider.
 */
export function PageForm({ onSubmit, busy, children }: { onSubmit: () => void; busy: boolean; children: ReactNode }) {
  const { blocked } = useFormValidity()
  return (
    <form
      className="contents" noValidate
      onSubmit={e => { e.preventDefault(); if (!blocked && !busy) onSubmit() }}
      onKeyDown={e => {
        const el = e.target as HTMLElement
        // A picker's input (role=combobox) uses Enter to choose; only a plain text box saves.
        if (e.key !== 'Enter' || e.defaultPrevented || el.tagName !== 'INPUT' || el.getAttribute('role')) return
        const type = (el as HTMLInputElement).type
        if (type !== 'text' && type !== 'url' && type !== 'search') return
        e.preventDefault()
        e.currentTarget.requestSubmit()
      }}
    >
      {children}
    </form>
  )
}
