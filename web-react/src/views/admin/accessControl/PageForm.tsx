import type { ReactNode } from 'react'

/**
 * The form element of an editor page. Saving is a deliberate click on the SaveBar's button, never a keystroke: Enter
 * in a text box must not write (it confirms an input-method candidate in zh-CN, and this area decides what the nodes
 * block), so a submit is swallowed here and the browser's own implicit submission is never wired to the save.
 * `display: contents` keeps the form out of the layout.
 */
export function PageForm({ children }: { children: ReactNode }) {
  return <form className="contents" noValidate onSubmit={e => e.preventDefault()}>{children}</form>
}
