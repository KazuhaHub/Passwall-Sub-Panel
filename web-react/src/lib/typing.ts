// "IS THE USER TYPING?", answered once for every global keyboard shortcut. A shortcut
// that fires while text is being entered eats the user's keystroke (Ctrl/Cmd+B is
// "bold" in an editor; on macOS Ctrl+K in a text field is "delete to end of line"), so
// each one asks this before it acts.
//
// Typing means a field that takes TEXT: a textarea, an <input> of a text-entry type
// (or none, which is text), a contenteditable region, or the code editor. A checkbox,
// radio, button, range, colour or file input is an <input> too but takes no text, and a
// shortcut must still work while one has focus (on Windows Ctrl+K did not open the
// palette from a focused checkbox). A <select> is likewise not text entry.

const NON_TEXT_INPUT = new Set(['checkbox', 'radio', 'button', 'submit', 'reset', 'image', 'range', 'color', 'file', 'hidden'])

function isTextEntry(el: Element): boolean {
  if (el.closest('.cm-editor')) return true
  const editable = el.closest('[contenteditable]')
  if (editable && editable.getAttribute('contenteditable') !== 'false') return true
  // An ARIA textbox (a custom editor) takes text however it is built.
  if (el.closest('[role="textbox"]')) return true
  const field = el.closest('input, textarea')
  if (!field) return false
  // A read-only or disabled field takes no keystrokes, so a shortcut may still fire from it.
  if ((field as HTMLInputElement).readOnly || (field as HTMLInputElement).disabled) return false
  if (field.tagName === 'TEXTAREA') return true
  return !NON_TEXT_INPUT.has((field as HTMLInputElement).type)
}

/**
 * The element a key press really went to. `event.target` is retargeted to the shadow
 * host when the field lives inside a shadow root; the first entry of the composed path is
 * the real one.
 */
export function eventOrigin(event: Event): EventTarget | null {
  return event.composedPath?.()[0] ?? event.target
}

/** True when the target is a text-entry field, a contenteditable region or the code editor. */
export function isTypingTarget(target: EventTarget | null): boolean {
  return target instanceof Element && isTextEntry(target)
}

/** The code editor specifically: it claims more shortcuts than a plain field does. */
export function isCodeEditorTarget(target: EventTarget | null): boolean {
  return target instanceof Element && target.closest('.cm-editor') !== null
}
