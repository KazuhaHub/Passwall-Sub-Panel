import { useEffect, useMemo, useState } from 'react'
import { previewLineVisibility, type LineVisibilityDraft, type LineVisibilityView } from '@/api/nodes'

const DEBOUNCE_MS = 400

export type LineVisibilityState =
  | { status: 'idle' }
  | { status: 'loading'; data?: LineVisibilityView }
  | { status: 'ready'; data: LineVisibilityView }
  | { status: 'error' }

/**
 * Asks the server which groups would see each draft line (ADR 0041) as the
 * editor's typing pauses. A failed read is reported as `error`, never as an
 * empty answer, so the editor can tell "no group" from "unknown".
 */
export function useLineVisibility(nodeId: number, draft: LineVisibilityDraft | null): LineVisibilityState {
  const key = useMemo(() => (draft ? JSON.stringify(draft) : ''), [draft])
  const [state, setState] = useState<LineVisibilityState>({ status: 'idle' })
  useEffect(() => {
    if (!nodeId || !key) { setState({ status: 'idle' }); return }
    const ctl = new AbortController()
    setState(previous => ({ status: 'loading', data: previous.status === 'ready' || previous.status === 'loading' ? previous.data : undefined }))
    const timer = setTimeout(() => {
      previewLineVisibility(nodeId, JSON.parse(key) as LineVisibilityDraft, ctl.signal)
        .then(data => { if (!ctl.signal.aborted) setState({ status: 'ready', data }) })
        .catch(() => { if (!ctl.signal.aborted) setState({ status: 'error' }) })
    }, DEBOUNCE_MS)
    return () => { clearTimeout(timer); ctl.abort() }
  }, [nodeId, key])
  return state
}

/** One line's visibility, resolved to group names for display. */
export type LineVisibilityHint = { state: 'loading' } | { state: 'error' } | { state: 'ready'; groups: string[] }

export function lineVisibilityHint(state: LineVisibilityState, kind: 'relays' | 'routes', index: number): LineVisibilityHint | undefined {
  if (state.status === 'idle') return undefined
  if (state.status === 'error') return { state: 'error' }
  const data = state.data
  if (!data) return { state: 'loading' }
  const ids = data[kind][index]
  if (!ids) return { state: 'loading' }
  const names = new Map(data.groups.map(group => [group.id, group.name]))
  return { state: 'ready', groups: ids.map(id => names.get(id) ?? `#${id}`) }
}
