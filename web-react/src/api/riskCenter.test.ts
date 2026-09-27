import fs from 'node:fs'
import { beforeEach, expect, it, vi } from 'vitest'

const http = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('./client', () => ({ client: http }))
import { refreshLiveConnections } from './riskCenter'

/**
 * The server's own bound on one refresh (riskcenter.liveRefreshTimeout), read
 * from the Go source rather than copied, so the two cannot drift apart
 * silently: a server bound raised past the SPA's timeout fails here, not in
 * front of an admin.
 */
function serverRefreshBoundMs(): number {
  const src = fs.readFileSync(
    new URL('../../../internal/service/riskcenter/riskcenter.go', import.meta.url), 'utf8')
  const m = /liveRefreshTimeout\s*=\s*(\d+)\s*\*\s*time\.Second/.exec(src)
  if (!m) throw new Error('riskcenter.go no longer declares liveRefreshTimeout as N * time.Second; update this test')
  return Number(m[1]) * 1000
}

beforeEach(() => {
  vi.clearAllMocks()
  http.post.mockResolvedValue({ data: { refreshed: true, reason: '' } })
})

// A refresh on a hung panel legitimately runs up to the server's bound (and
// it is not cancelled by the browser giving up: it still stores its reading
// and spends the fleet-wide cooldown). Aborted at the shared client's 30 s,
// the page would report a timeout for a refresh that did happen, and the
// admin's retry would only meet the cooldown. So this one request waits
// longer than the server can take.
it('waits longer for a refresh than the server lets one run', async () => {
  await refreshLiveConnections()
  expect(http.post).toHaveBeenCalledOnce()
  const [url, body, cfg] = http.post.mock.calls[0] as [string, unknown, { timeout?: number; _skipErrorToast?: boolean }]
  expect(url).toBe('/admin/risk-center/live/refresh')
  expect(body).toBeUndefined()
  expect(cfg._skipErrorToast).toBe(true)
  expect(cfg.timeout, 'the refresh POST carries its own timeout').toBeTypeOf('number')
  expect(cfg.timeout).toBeGreaterThan(serverRefreshBoundMs())
})
