import { useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { useBlocker } from 'react-router'

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
 * Warns before unsaved edits are lost: on a route change (react-router's
 * blocker) and on closing or reloading the tab (the browser's beforeunload
 * prompt, whose text the browser controls). It guards ROUTE changes only; a
 * dialog or sheet closed with Esc is not a navigation and needs its own guard.
 *
 * useBlocker needs a data router (createBrowserRouter), which production mounts.
 */
export function useUnsavedChanges(dirty: boolean, opts: UnsavedChangesOptions = {}): void {
  const { t } = useTranslation('common')
  const leaves = opts.leaves ?? ((next, current) => next.pathname !== current.pathname)
  const blocker = useBlocker(({ currentLocation, nextLocation }) => dirty && leaves(nextLocation, currentLocation))

  // Read through a ref so the effect below runs once per blocked navigation,
  // not once per render while the question is open.
  const ask = useRef<() => Promise<boolean>>(async () => true)
  useEffect(() => {
    ask.current = opts.confirm ?? (async () => window.confirm(t('ui.form.unsaved_leave')))
  })

  useEffect(() => {
    if (blocker.state !== 'blocked') return
    let live = true
    void ask.current().then(ok => {
      if (!live) return
      if (ok) blocker.proceed()
      else blocker.reset()
    })
    return () => { live = false }
  }, [blocker])

  useEffect(() => {
    if (!dirty) return
    const onBeforeUnload = (e: BeforeUnloadEvent) => { e.preventDefault() }
    window.addEventListener('beforeunload', onBeforeUnload)
    return () => window.removeEventListener('beforeunload', onBeforeUnload)
  }, [dirty])
}
