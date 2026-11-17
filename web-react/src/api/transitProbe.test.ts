import { beforeEach, describe, expect, it, vi } from 'vitest'

const http = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }))
vi.mock('./client', () => ({ client: http }))

import * as api from './transitProbe'

beforeEach(() => vi.clearAllMocks())

describe('transit probe api', () => {
  it('reads the settings view and defaults a missing defaults map', async () => {
    http.get.mockResolvedValue({ data: { settings: { transit_probe_enabled: true }, effective: {} } })
    const v = await api.getTransitProbeSettings()
    expect(http.get).toHaveBeenCalledWith('/admin/transit/probe-settings', expect.objectContaining({ _skipErrorToast: true }))
    expect(v.defaults).toEqual({})
  })
  it('puts only the changed keys, wrapped in settings', async () => {
    http.put.mockResolvedValue({ data: { settings: {}, defaults: {}, effective: {} } })
    await api.putTransitProbeSettings({ transit_probe_withhold: true })
    expect(http.put).toHaveBeenCalledWith('/admin/transit/probe-settings', { settings: { transit_probe_withhold: true } }, expect.anything())
  })
  it('lists probes, null as empty, with the panel filter', async () => {
    http.get.mockResolvedValue({ data: { probes: null } })
    expect(await api.getTransitProbes(3)).toEqual([])
    expect(http.get).toHaveBeenCalledWith('/admin/transit/probes', expect.objectContaining({ params: { panel_id: 3 } }))
  })
})
