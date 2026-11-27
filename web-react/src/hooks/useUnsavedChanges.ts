import { useCallback, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { useGuardRegistration } from './useLeaveGuard'

export interface UnsavedChangesOptions {
  /**
   * Asks the person whether to leave; resolves true to leave. Pass the app's
   * confirm dialog so the prompt looks like every other one; without it the
   * browser's own dialog is used, which is plain but always works.
   */
  confirm?: () => Promise<boolean>
  /**
   * Whether a navigation actually drops the draft. The default treats a change
   * of pathname as leaving and a query-string change (a tab, a filter, an opened
   * row) as staying, which is what a single page with URL state wants.
   */
  leaves?: (next: { pathname: string }, current: { pathname: string }) => boolean
}

/**
 * Warns before unsaved edits are lost: on a route change, including the
 * browser's Back and Forward (react-router's blocker), and on closing or
 * reloading the tab (the browser's beforeunload prompt, whose text the browser
 * controls). It guards ROUTE changes only; a
 * dialog or sheet closed with Esc is not a navigation and needs its own guard.
 *
 * useBlocker needs a data router (createBrowserRouter), which production mounts.
 */
export interface UnsavedChangesControls {
  /**
   * The draft was just saved. Takes effect immediately, before React renders the
   * `dirty = false` that the save also causes, so "save, then navigate" in one
   * handler is not stopped by a prompt about changes that no longer exist.
   * Call it AFTER the save succeeded. If it was called too early and the save
   * failed, the next render with `dirty` still true arms the guard again.
   */
  markSaved: () => void
  /**
   * Let the navigation you are about to start through without asking: call it
   * synchronously right before navigating, with no `await` in between. It expires when
   * the tick ends, so a later, unrelated route change is guarded as usual.
   */
  allowNext: () => void
}

export function useUnsavedChanges(dirty: boolean, opts: UnsavedChangesOptions = {}): UnsavedChangesControls {
  const { t } = useTranslation('common')
  const leaves = opts.leaves ?? ((next, current) => next.pathname !== current.pathname)

  // What the guard reads is a ref, updated synchronously, never the `dirty` of
  // the render the blocker callback happened to be created in.
  const dirtyRef = useRef(dirty)
  const allowRef = useRef(false)
  // Every render restates what the page says: a markSaved() that was premature (the
  // save then failed) is undone by the next render that still has dirty = true.
  dirtyRef.current = dirty
  const leavesRef = useRef(leaves)
  leavesRef.current = leaves

  // Registered with the shared guard registry (hooks/useLeaveGuard): one blocker for the whole router, however many guards are mounted.
  useGuardRegistration({
    // The ref, not the prop: markSaved() clears it before React renders the save.
    get dirty() { return dirtyRef.current },
    leaves: (next, current) => {
      if (allowRef.current) { allowRef.current = false; return false }
      return leavesRef.current(next, current)
    },
    ask: opts.confirm ?? (async () => window.confirm(t('ui.form.unsaved_leave'))),
  })

  return {
    markSaved: useCallback(() => { dirtyRef.current = false }, []),
    allowNext: useCallback(() => { allowRef.current = true; queueMicrotask(() => { allowRef.current = false }) }, []),
  }
}
