import i18n from '@/i18n'
import { pushSnack } from '@/components/SnackbarHost'

// copyToClipboard hardens the typical `navigator.clipboard.writeText`
// path with:
//   • a document.execCommand fallback for HTTP / non-secure contexts
//     where the modern API isn't available
//   • a user-facing toast on success AND on failure so the action
//     never silently no-ops — the previous catch{} swallow made the
//     "Copy" button feel broken on HTTP deployments
//
// Returns true on success so the caller can chain UI state changes.
export async function copyToClipboard(text: string): Promise<boolean> {
  const t = i18n.t
  // Modern path — requires a Secure Context (https or localhost) and
  // user gesture. Throws or returns rejected promise on permission deny.
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text)
      pushSnack(t('common:errors.copied', { defaultValue: 'Copied to clipboard.' }), 'success')
      return true
    }
  } catch {
    // fall through to execCommand fallback
  }
  // Legacy fallback — works on HTTP, but only inside a user gesture
  // (click / keydown). Creates an off-screen textarea, selects it,
  // execCommand('copy'), removes it.
  try {
    const ta = document.createElement('textarea')
    ta.value = text
    ta.setAttribute('readonly', '')
    ta.style.position = 'absolute'
    ta.style.left = '-9999px'
    ta.style.top = '0'
    document.body.appendChild(ta)
    ta.select()
    const ok = document.execCommand('copy')
    document.body.removeChild(ta)
    if (ok) {
      pushSnack(t('common:errors.copied', { defaultValue: 'Copied to clipboard.' }), 'success')
      return true
    }
  } catch {
    // intentional fallthrough — handled below
  }
  pushSnack(t('common:errors.copy_failed', { defaultValue: 'Copy failed.' }), 'warning')
  return false
}
