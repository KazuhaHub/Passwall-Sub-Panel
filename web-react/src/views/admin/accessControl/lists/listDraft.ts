import type { DestinationListKind, DestinationListSummary } from '@/api/accessControl'
export const MAX_CUSTOM_BYTES = 4 * 1024 * 1024
export function listSourceLabel(list: DestinationListSummary): string {
  if (list.kind === 'geosite') return list.geosite_category
  if (list.kind === 'remote') { try { return new URL(list.source_url).hostname } catch { return list.source_url } }
  return ''
}
export function validRemoteURL(input: string): boolean {
  try { const url = new URL(input); return url.protocol === 'https:' && !!url.hostname && !url.username && !url.password } catch { return false }
}
export function listIsProblem(list: DestinationListSummary, enabledPolicyIds: ReadonlySet<number>): boolean {
  return list.state === 'failed' || list.state === 'pending' && list.used_by.some(ref => ref.kind === 'policy' && enabledPolicyIds.has(ref.id))
}
export function listPreviewBlocksSave(kind: DestinationListKind, error: string, count?: number): boolean {
  if (error === 'dest_list_too_large') return true
  if (kind === 'geosite' && error) return true
  if (kind === 'remote' && error && error !== 'dest_list_fetch_failed') return true
  return kind !== 'custom' && count === 0
}
export function listContentImpact(used: number, before?: string, after?: string): 'unused' | 'unchanged' | 'changes' | 'unknown' {
  if (!used) return 'unused'
  if (!before || !after) return 'unknown'
  return before === after ? 'unchanged' : 'changes'
}
