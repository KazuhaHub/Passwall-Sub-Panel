import { isAxiosError } from 'axios'
export function destinationError(error: unknown): { status?: number; error: string; field?: string } {
  if (isAxiosError(error)) {
    const rawField = error.response?.data?.field as string | undefined
    return { status: error.response?.status, error: String(error.response?.data?.error ?? error.message),
      field: rawField?.replace(/^policy\./, '').replace(/^inline\./, '') }
  }
  return { error: error instanceof Error ? error.message : String(error) }
}
