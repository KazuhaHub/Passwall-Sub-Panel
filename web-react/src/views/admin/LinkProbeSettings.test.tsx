// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { api, installReads, mount, snack } from '@/test/adminSaveHarness'
import LinkProbeSettings from './LinkProbeSettings'

const settings = { transit_probe_enabled: true, transit_probe_interval_seconds: 0, transit_probe_timeout_ms: 0, transit_probe_url: 'https://www.gstatic.com/generate_204', transit_probe_withhold: false, transit_probe_withhold_after: 0, transit_probe_restore_after: 0 }
const defaults = { transit_probe_interval_seconds: 60, transit_probe_timeout_ms: 5000, transit_probe_url: 'https://www.gstatic.com/generate_204', transit_probe_withhold_after: 3, transit_probe_restore_after: 3 }
const view = (over = {}) => ({ settings: { ...settings, ...over }, defaults, effective: { ...settings, ...over } })

async function open(over = {}) {
  installReads({ '/admin/transit/probe-settings': view(over) })
  mount(<LinkProbeSettings />)
  await screen.findByLabelText('admin:settings.probe.interval')
}
const field = (k: string) => screen.getByLabelText(`admin:settings.probe.${k}`) as HTMLInputElement
const saveBtn = () => screen.getByRole('button', { name: 'admin:settings.probe.save' }) as HTMLButtonElement

describe('LinkProbeSettings', () => {
  it('renders values and defaults as placeholders', async () => {
    await open({ transit_probe_interval_seconds: 120, transit_probe_withhold: true })
    expect(field('interval').value).toBe('120')
    expect(field('timeout').value).toBe('')
    expect(field('timeout').placeholder).toBe('5000')
    expect(field('url').placeholder).toBe(defaults.transit_probe_url)
    expect((screen.getByLabelText('admin:settings.probe.withhold') as HTMLInputElement).checked).toBe(true)
    expect(screen.getAllByRole('button', { name: /admin:settings\.probe\..*_help/ })).toHaveLength(7)
    expect(saveBtn().disabled).toBe(true)
  })

  it('shows the actual url and keeps empty as transport-only, never substituting the default', async () => {
    await open()
    expect(field('url').value).toBe(defaults.transit_probe_url)
    expect(saveBtn().disabled).toBe(true)
    api.put.mockResolvedValueOnce({ data: view({ transit_probe_url: '' }) })
    fireEvent.change(field('url'), { target: { value: '' } })
    expect(screen.queryByText('admin:settings.probe.err_url')).toBeNull()
    expect(saveBtn().disabled).toBe(false)
    fireEvent.click(saveBtn())
    await waitFor(() => expect(api.put).toHaveBeenCalledOnce())
    expect(api.put).toHaveBeenCalledWith('/admin/transit/probe-settings', { settings: { transit_probe_url: '' } }, expect.anything())
    await waitFor(() => expect(snack).toHaveBeenCalledWith('admin:settings.probe.saved', 'success'))
    // Adopted as empty: the field stays empty and the default is only a placeholder.
    expect(field('url').value).toBe('')
    expect(field('url').placeholder).toBe(defaults.transit_probe_url)
    // The reset hint puts the default back into the draft on request only.
    fireEvent.click(screen.getByRole('button', { name: 'admin:settings.probe.url_reset' }))
    expect(field('url').value).toBe(defaults.transit_probe_url)
    expect(saveBtn().disabled).toBe(false)
  })

  it('puts only the changed keys and adopts the response', async () => {
    await open()
    api.put.mockResolvedValueOnce({ data: view({ transit_probe_timeout_ms: 2000, transit_probe_withhold: true }) })
    fireEvent.change(field('timeout'), { target: { value: '2000' } })
    fireEvent.click(screen.getByLabelText('admin:settings.probe.withhold'))
    fireEvent.click(saveBtn())
    await waitFor(() => expect(api.put).toHaveBeenCalledOnce())
    expect(api.put).toHaveBeenCalledWith('/admin/transit/probe-settings',
      { settings: { transit_probe_timeout_ms: 2000, transit_probe_withhold: true } }, expect.anything())
    await waitFor(() => expect(snack).toHaveBeenCalledWith('admin:settings.probe.saved', 'success'))
    expect(saveBtn().disabled).toBe(true)
  })

  it('blocks save on out-of-range and bad url values', async () => {
    await open()
    fireEvent.change(field('interval'), { target: { value: '10' } })
    expect(screen.getByText('admin:settings.probe.err_range')).toBeTruthy()
    expect(saveBtn().disabled).toBe(true)
    fireEvent.change(field('interval'), { target: { value: '0' } })
    fireEvent.change(field('url'), { target: { value: 'ftp://x' } })
    expect(screen.getByText('admin:settings.probe.err_url')).toBeTruthy()
    fireEvent.change(field('url'), { target: { value: 'http://example.test:70000/x' } })
    expect(screen.getByText('admin:settings.probe.err_url')).toBeTruthy()
    fireEvent.change(field('url'), { target: { value: 'http://example.test:8080/x' } })
    expect(screen.queryByText('admin:settings.probe.err_url')).toBeNull()
    expect(saveBtn().disabled).toBe(false)
    expect(api.put).not.toHaveBeenCalled()
  })

  it('shows a server 400 under the named field', async () => {
    await open()
    api.put.mockRejectedValueOnce(Object.assign(new Error('bad'), {
      isAxiosError: true,
      response: { status: 400, data: { error: 'unreachable url', field: 'transit_probe_url', code: 'transit_probe_settings_invalid' } },
    }))
    fireEvent.change(field('url'), { target: { value: 'https://probe.example.test' } })
    fireEvent.click(saveBtn())
    expect(await screen.findByText('unreachable url')).toBeTruthy()
    expect(field('url').getAttribute('aria-invalid')).toBe('true')
  })
})
