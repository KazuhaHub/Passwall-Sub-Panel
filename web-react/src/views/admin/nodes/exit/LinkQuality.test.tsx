// @vitest-environment jsdom
import { screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { api, installReads, mountPage } from '../testHarness'
import { LinkQuality } from './LinkQuality'

const probe = (over = {}) => ({
  key: 'lnk_1', kind: 'e2e', entry_panel_id: 1, exit_panel_id: 2, state: 'ok',
  rtt_median_ms: 40, loss_percent: 0, withheld: false, ...over,
})

describe('LinkQuality', () => {
  it('prefers the end-to-end item over transport for the pair', async () => {
    installReads({ '/admin/transit/probes': { probes: [
      probe({ kind: 'transport', state: 'down' }),
      probe({ kind: 'e2e', state: 'degraded' }),
      probe({ key: 'lnk_2', exit_panel_id: 3, state: 'down' }),
    ] } })
    mountPage(<LinkQuality entry={1} exit={2} />)
    const chip = await screen.findByTestId('link-quality')
    expect(chip.dataset.state).toBe('degraded')
    expect(api.get).toHaveBeenCalledWith('/admin/transit/probes', expect.objectContaining({ params: { panel_id: 1 } }))
  })
  it('falls back to transport and shows a stale item as no data', async () => {
    installReads({ '/admin/transit/probes': { probes: [probe({ kind: 'transport', state: 'ok', stale: true })] } })
    mountPage(<LinkQuality entry={1} exit={2} />)
    expect((await screen.findByTestId('link-quality')).dataset.state).toBe('unknown')
  })
  it('shows a dash when the pair has no probe', async () => {
    installReads({ '/admin/transit/probes': { probes: [probe({ exit_panel_id: 9 })] } })
    mountPage(<LinkQuality entry={1} exit={2} />)
    expect(await screen.findByTestId('link-quality-none')).toBeTruthy()
    expect(screen.queryByTestId('link-quality')).toBeNull()
  })
  it('shows a dash when the load fails', async () => {
    installReads({})
    mountPage(<LinkQuality entry={1} exit={2} />)
    expect(await screen.findByTestId('link-quality-none')).toBeTruthy()
  })
})
