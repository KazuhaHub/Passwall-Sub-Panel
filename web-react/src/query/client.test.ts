import { describe, expect, it } from 'vitest'
import { shouldRetryQuery } from './client'

/** axios's isAxiosError only checks this flag, so a plain object is enough. */
function axiosError(opts: { status?: number; code?: string }) {
  return {
    isAxiosError: true,
    code: opts.code,
    response: opts.status === undefined ? undefined : { status: opts.status },
  }
}

describe('shouldRetryQuery', () => {
  it('retries a network failure exactly once', () => {
    expect(shouldRetryQuery(0, axiosError({ code: 'ERR_NETWORK' }))).toBe(true)
    expect(shouldRetryQuery(1, axiosError({ code: 'ERR_NETWORK' }))).toBe(false)
  })

  it('retries a recoverable 5xx once', () => {
    expect(shouldRetryQuery(0, axiosError({ status: 500 }))).toBe(true)
    expect(shouldRetryQuery(0, axiosError({ status: 502 }))).toBe(true)
    expect(shouldRetryQuery(1, axiosError({ status: 500 }))).toBe(false)
  })

  it('does not retry the deterministic capability answers', () => {
    // 501 "unsupported" and 503 "capability not configured" are final answers,
    // not transient faults — retrying only delays the error the UI must show.
    expect(shouldRetryQuery(0, axiosError({ status: 501 }))).toBe(false)
    expect(shouldRetryQuery(0, axiosError({ status: 503 }))).toBe(false)
  })

  it('does not retry client errors or rate limiting', () => {
    for (const status of [400, 401, 403, 404, 422, 429]) {
      expect(shouldRetryQuery(0, axiosError({ status }))).toBe(false)
    }
  })

  it('does not retry a cancelled request', () => {
    // Cancellation is normal control flow — a retry would resurrect it.
    expect(shouldRetryQuery(0, axiosError({ code: 'ERR_CANCELED' }))).toBe(false)
  })

  it('does not retry a non-axios throw', () => {
    // A plain Error is a programming mistake, not a transient fault.
    expect(shouldRetryQuery(0, new Error('boom'))).toBe(false)
  })
})
