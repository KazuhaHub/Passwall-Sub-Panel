import { isAxiosError } from 'axios'
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
