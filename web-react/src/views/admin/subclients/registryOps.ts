// The edits the Sub clients page makes to the family list, as pure functions on the array. Each returns a
// new array and touches nothing it was not asked to, so the saved order and values are exactly what the
// admin arranged (the list is ORDER-SENSITIVE: the first family whose keywords match a User-Agent wins).
import type { SubClientApp, SubClientFamily, SubPlatform } from '@/api/settings'

export const blankFamily = (): SubClientFamily => ({ name: '', keywords: [], render_format: 'mihomo', enabled: true, apps: [] })

/** The keyword box is plain text; what is saved is the comma-separated parts, trimmed, empties dropped. */
export const parseKeywords = (text: string): string[] => text.split(',').map(s => s.trim()).filter(Boolean)

export const patchFamily = (list: SubClientFamily[], fi: number, patch: Partial<SubClientFamily>): SubClientFamily[] =>
  list.map((f, i) => (i === fi ? { ...f, ...patch } : f))

export const removeFamily = (list: SubClientFamily[], fi: number): SubClientFamily[] => list.filter((_, i) => i !== fi)

export const patchApp = (list: SubClientFamily[], fi: number, ai: number, patch: Partial<SubClientApp>): SubClientFamily[] =>
  list.map((f, i) => (i !== fi ? f : { ...f, apps: f.apps.map((a, j) => (j === ai ? { ...a, ...patch } : a)) }))

export const removeApp = (list: SubClientFamily[], fi: number, ai: number): SubClientFamily[] =>
  list.map((f, i) => (i !== fi ? f : { ...f, apps: f.apps.filter((_, j) => j !== ai) }))

/** A new app sorts after the family's last one (or after the family's own slot, fi * 10, when it has none). */
export const addApp = (list: SubClientFamily[], fi: number): SubClientFamily[] =>
  list.map((f, i) => (i !== fi ? f : {
    ...f,
    apps: [...f.apps, { name: '', platforms: [], import_url_template: '', install_url: '', enabled: true, sort: (f.apps.at(-1)?.sort ?? fi * 10) + 10, recommended_for: [] }],
  }))

/** Turning a platform off also takes it out of recommended_for: an app cannot be recommended where it does not run. */
export function togglePlatform(app: SubClientApp, p: SubPlatform): Pick<SubClientApp, 'platforms' | 'recommended_for'> {
  const selected = app.platforms.includes(p)
  const platforms = selected ? app.platforms.filter(x => x !== p) : [...app.platforms, p]
  return { platforms, recommended_for: (app.recommended_for ?? []).filter(x => platforms.includes(x)) }
}

export function toggleRecommended(app: SubClientApp, p: SubPlatform): SubPlatform[] {
  const cur = app.recommended_for ?? []
  return cur.includes(p) ? cur.filter(x => x !== p) : [...cur, p]
}
