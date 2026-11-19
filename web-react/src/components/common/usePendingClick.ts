import { useEffect, useRef, useState, type MouseEvent } from 'react'

export type ClickHandler = (event: MouseEvent<HTMLButtonElement>) => unknown

function isThenable(value: unknown): value is PromiseLike<unknown> {
  return typeof (value as PromiseLike<unknown> | null)?.then === 'function'
}

// BUTTONS THAT SHOW THEIR OWN WAIT. On a slow link a row action (delete, renew,
// reset, retry) answered seconds after the click, and until then the control sat
// unchanged and clickable, so it read as a dead button and invited a second,
// duplicate request. This tracks the promise the handler returns: while it is
// pending the button is disabled and marked aria-busy, and further clicks are
// ignored. A handler that returns nothing behaves as a plain button.
//
// A rejected action only ends the wait. The failure itself is the caller's to
// report, and the shared client already toasts request errors.
export function usePendingClick(onClick: ClickHandler | undefined) {
  const [pending, setPending] = useState(false)
  // A ref as well as state: two clicks inside one frame both see the state
  // from before the first one re-rendered.
  const running = useRef(false)
  const mounted = useRef(true)
  // Set on setup as well as cleared on cleanup: StrictMode replays effects on
  // mount (setup, cleanup, setup), and a flag only the cleanup wrote would end
  // that replay as "unmounted" and leave the button busy after its first action.
  useEffect(() => {
    mounted.current = true
    return () => { mounted.current = false }
  }, [])

  async function handle(event: MouseEvent<HTMLButtonElement>) {
    if (running.current || !onClick) return
    const result = onClick(event)
    if (!isThenable(result)) return
    running.current = true
    setPending(true)
    try {
      await result
    } catch {
      // Reported by whoever started the action; see the note above.
    } finally {
      running.current = false
      if (mounted.current) setPending(false)
    }
  }
  return { pending, handle }
}
