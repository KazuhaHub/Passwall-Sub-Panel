import { useMemo } from 'react'
import { previewLineVisibility, type LineVisibilityDraft, type LineVisibilityView } from '@/api/nodes'
import { previewHint, useDraftPreview, type DraftPreviewState, type PreviewHint } from '@/hooks/useDraftPreview'

export type LineVisibilityState = DraftPreviewState<LineVisibilityView>

/** Which groups would see each draft line (ADR 0041), via the shared preview hook. */
export function useLineVisibility(nodeId: number, draft: LineVisibilityDraft | null): LineVisibilityState {
  const key = useMemo(() => (nodeId && draft ? JSON.stringify([nodeId, draft]) : null), [nodeId, draft])
  return useDraftPreview(key, signal => previewLineVisibility(nodeId, draft as LineVisibilityDraft, signal))
}

/** The group names that would see one draft line. */
export function lineVisibilityHint(state: LineVisibilityState, kind: 'relays' | 'routes', index: number): PreviewHint | undefined {
  return previewHint(state, data => {
    const ids = data[kind][index]
    if (!ids) return undefined
    const names = new Map(data.groups.map(group => [group.id, group.name]))
    return ids.map(id => names.get(id) ?? `#${id}`)
  })
}

/** The groups that would see the node itself, and those this save removes it from. */
export function nodeGroupsHint(state: LineVisibilityState): { now?: PreviewHint; removed: string[] } {
  const now = previewHint(state, data => {
    const names = new Map(data.groups.map(group => [group.id, group.name]))
    return data.node_groups.map(id => names.get(id) ?? `#${id}`)
  })
  if (state.status !== 'ready') return { now, removed: [] }
  const names = new Map(state.data.groups.map(group => [group.id, group.name]))
  const kept = new Set(state.data.node_groups)
  return { now, removed: state.data.saved_node_groups.filter(id => !kept.has(id)).map(id => names.get(id) ?? `#${id}`) }
}
