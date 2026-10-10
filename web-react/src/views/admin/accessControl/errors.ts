import { isAxiosError } from 'axios'
import type { DestinationReference } from '@/api/accessControl'
export function destinationError(error: unknown): { status?: number; error: string; field?: string } {
  if (isAxiosError(error)) {
    const rawField = error.response?.data?.field as string | undefined
    return { status: error.response?.status, error: String(error.response?.data?.error ?? error.message),
      field: rawField?.replace(/^policy\./, '').replace(/^inline\./, '') }
  }
  return { error: error instanceof Error ? error.message : String(error) }
}
export function destinationListFailure(error: unknown): { httpStatus?: number; bad: Array<{ line: number; entry: string }> } {
  if (!isAxiosError(error)) return { bad: [] }
  const data = error.response?.data
  return { httpStatus: typeof data?.http_status === 'number' && data.http_status > 0 ? data.http_status : undefined,
    bad: Array.isArray(data?.bad) ? data.bad.slice(0, 20).filter((item: unknown): item is { line: number; entry: string } => !!item && typeof item === 'object' && 'line' in item && typeof item.line === 'number' && 'entry' in item && typeof item.entry === 'string') : [] }
}
export function destinationListReferences(error: unknown): DestinationReference[] {
  if (!isAxiosError(error) || !Array.isArray(error.response?.data?.used_by)) return []
  const seen = new Set<string>()
  return error.response.data.used_by.filter((ref: unknown): ref is DestinationReference => {
    if (!ref || typeof ref !== 'object' || !('kind' in ref) || ref.kind !== 'policy' && ref.kind !== 'group' ||
      !('id' in ref) || typeof ref.id !== 'number' || !Number.isSafeInteger(ref.id) || ref.id <= 0 ||
      !('name' in ref) || typeof ref.name !== 'string') return false
    const key = `${ref.kind}-${ref.id}`
    if (seen.has(key)) return false
    seen.add(key)
    return true
  }).map((ref: DestinationReference) => ({ kind: ref.kind, id: ref.id, name: ref.name.trim() || `#${ref.id}` }))
}
