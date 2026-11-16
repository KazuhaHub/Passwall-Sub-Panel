import { useEffect, useRef, useState } from 'react'

const DEFAULT_DELAY_MS = 400

export type DraftPreviewState<T> =
  | { status: 'idle' }
  | { status: 'loading'; data?: T }
  | { status: 'ready'; data: T }
  | { status: 'error' }

/**
 * Edit-time effect preview (ADR 0042 §6): asks the server what a draft would
 * do once typing pauses, cancels stale requests, and never caches the answer.
 * A pending request keeps the previous answer so the caption does not flash
 * empty; a failed or malformed read is `error`, never an empty answer.
 *
 * `draftKey` identifies the draft (null disables the preview); the fetcher is
 * read from a ref, so a new closure each render does not refetch.
 */
export function useDraftPreview<T>(draftKey: string | null, fetcher: (signal: AbortSignal) => Promise<T>, delayMs = DEFAULT_DELAY_MS): DraftPreviewState<T> {
  const [state, setState] = useState<DraftPreviewState<T>>({ status: 'idle' })
  const fetcherRef = useRef(fetcher)
  fetcherRef.current = fetcher
  useEffect(() => {
    if (!draftKey) { setState({ status: 'idle' }); return }
    const ctl = new AbortController()
    setState(previous => ({ status: 'loading', data: previous.status === 'ready' || previous.status === 'loading' ? previous.data : undefined }))
    const timer = setTimeout(() => {
      fetcherRef.current(ctl.signal)
        .then(data => { if (!ctl.signal.aborted) setState({ status: 'ready', data }) })
        .catch(() => { if (!ctl.signal.aborted) setState({ status: 'error' }) })
    }, delayMs)
    return () => { clearTimeout(timer); ctl.abort() }
  }, [draftKey, delayMs])
  return state
}

/** One preview item list, resolved for display. */
export type PreviewHint = { state: 'loading' } | { state: 'error' } | { state: 'ready'; items: string[] }

/** Maps a preview state to a hint with `pick`; a missing row is still loading. */
export function previewHint<T>(state: DraftPreviewState<T>, pick: (data: T) => string[] | undefined): PreviewHint | undefined {
  if (state.status === 'idle') return undefined
  if (state.status === 'error') return { state: 'error' }
  if (!state.data) return { state: 'loading' }
  const items = pick(state.data)
  return items ? { state: 'ready', items } : { state: 'loading' }
}
