/** What a failed request says, for a line in a save bar: the server's own message, else the error's. */
export function reasonOf(err: unknown): string {
  const e = err as { response?: { data?: { error?: string } }; message?: string }
  return e.response?.data?.error || e.message || ''
}
