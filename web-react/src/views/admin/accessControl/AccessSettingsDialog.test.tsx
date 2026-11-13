// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import * as api from '@/api/accessControl'
import { confirmMock, renderAccess } from '@/test/accessControlHarness'
import AccessSettingsDialog from './AccessSettingsDialog'

vi.mock('@/api/accessControl', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/accessControl')>()
  const fns = Object.fromEntries(Object.keys(real).filter(k => typeof (real as Record<string, unknown>)[k] === 'function').map(k => [k, vi.fn()]))
  return { ...real, ...fns, destErrorOf: real.destErrorOf, isDestUnavailable: real.isDestUnavailable }
})

const view = (over: Partial<api.DestSettings> = {}): api.DestSettingsView => ({
  settings: { dest_policy_apply_min_seconds: 0, dest_list_refresh_hours: 0, dest_rules_update_mode: '', ...over },
  defaults: { dest_policy_apply_min_seconds: 60, dest_list_refresh_hours: 24 },
  effective: { dest_policy_apply_min_seconds: 60, dest_list_refresh_hours: 24, dest_rules_update_mode: 'manual' },
})
const meta = (tag: string): api.GeositeMeta => ({ tag, sha256: 'x', downloaded_at: '2026-11-01T00:00:00Z', bytes: 1 })
const geo = (over: Partial<api.GeositeStatus> = {}): api.GeositeStatus => ({
  current: meta('20261101'), previous: meta('20261020'), current_released_at: '2026-11-01T00:00:00Z', current_age_days: 12,
  latest: { tag: '20261112' }, update_available: true, behind_days: 11, mode: 'manual', ...over,
})
const diff = (over: Partial<api.GeositeUpdateResult> = {}): api.GeositeUpdateResult => ({
  dry_run: true, from: meta('20261101'), to: meta('20261112'), unchanged: false, applied: false, lists_changed: 1, bumped: false,
  categories: [{ name: 'category-porn', old: 100, new: 110, added: 12, removed: 2, state: 'changed', in_use: true }], ...over,
})

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.getDestSettings).mockResolvedValue(view())
  vi.mocked(api.getGeositeStatus).mockResolvedValue(geo())
})

const open = () => renderAccess(<AccessSettingsDialog open onClose={onClose} />)
const onClose = vi.fn()

describe('settings', () => {
  it('shows blanks with the served defaults as placeholders, never a 0, and sends only the changed key', async () => {
    vi.mocked(api.putDestSettings).mockResolvedValue(view({ dest_list_refresh_hours: 12 }))
    open()
    const refresh = await screen.findByLabelText('远程与分类列表刷新间隔（小时）')
    expect((refresh as HTMLInputElement).value).toBe('')
    expect((refresh as HTMLInputElement).placeholder).toBe('24')
    fireEvent.focus(refresh)
    fireEvent.change(refresh, { target: { value: '12' } })
    expect(await screen.findByText('已修改 1 项')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(api.putDestSettings).toHaveBeenCalledWith({ dest_list_refresh_hours: 12 }))
  })

  it('blocks a value outside the server range and puts a 400 on the key it names', async () => {
    vi.mocked(api.putDestSettings).mockRejectedValue(Object.assign(new Error('x'), {
      isAxiosError: true, response: { status: 400, data: { code: 'dest_settings_invalid', field: 'dest_policy_apply_min_seconds' } },
    }))
    open()
    const wait = await screen.findByLabelText('策略下发等待（秒）')
    fireEvent.focus(wait)
    fireEvent.change(wait, { target: { value: '5' } })
    expect((screen.getByRole('button', { name: '保存' }) as HTMLButtonElement).disabled).toBe(true)
    fireEvent.change(wait, { target: { value: '45' } })
    fireEvent.click(screen.getByRole('button', { name: '保存' }))
    expect(await screen.findByText('超出范围')).toBeTruthy()
  })

  it('asks before closing with unsaved changes', async () => {
    open()
    const wait = await screen.findByLabelText('策略下发等待（秒）')
    fireEvent.focus(wait)
    fireEvent.change(wait, { target: { value: '90' } })
    confirmMock.mockResolvedValueOnce(false)
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' })
    await waitFor(() => expect(confirmMock).toHaveBeenCalledWith(expect.objectContaining({ title: '放弃未保存的修改？' })))
    expect(onClose).not.toHaveBeenCalled()
  })

  it('saves the update mode as dest_rules_update_mode', async () => {
    vi.mocked(api.putDestSettings).mockResolvedValue(view({ dest_rules_update_mode: 'auto' }))
    open()
    fireEvent.click(await screen.findByRole('button', { name: '自动' }))
    fireEvent.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(api.putDestSettings).toHaveBeenCalledWith({ dest_rules_update_mode: 'auto' }))
  })

  it('shows a 503 as not wired', async () => {
    vi.mocked(api.getDestSettings).mockRejectedValue(Object.assign(new Error('x'), { isAxiosError: true, response: { status: 503, data: { code: 'dest_unavailable' } } }))
    open()
    expect(await screen.findByText('设置未接线')).toBeTruthy()
  })
})

describe('rule data', () => {
  it('shows the current tag, its age and the latest upstream', async () => {
    open()
    const panel = await screen.findByTestId('rule-data-panel')
    expect(panel.textContent).toContain('当前版本 20261101')
    expect(panel.textContent).toContain('12 天前')
    expect(panel.textContent).toContain('上游最新版本 20261112')
    expect(panel.textContent).toContain('落后 11 天')
  })

  it('previews with a dry run, then applies naming the reviewed tag', async () => {
    vi.mocked(api.updateGeosite).mockResolvedValueOnce(diff()).mockResolvedValueOnce({ ...diff(), dry_run: false, applied: true })
    open()
    fireEvent.click(await screen.findByRole('button', { name: '检查更新…' }))
    await screen.findByText(/^(更新规则数据？|回到上一版本？)$/)
    const dlg = (await screen.findAllByRole('dialog')).at(-1)!
    expect(within(dlg).getByText('category-porn')).toBeTruthy()
    expect(within(dlg).getByText('+12 −2')).toBeTruthy()
    expect(within(dlg).getByText('正在使用')).toBeTruthy()
    expect(api.updateGeosite).toHaveBeenCalledWith({ dryRun: true })
    fireEvent.click(within(dlg).getByRole('button', { name: '应用更新' }))
    await waitFor(() => expect(api.updateGeosite).toHaveBeenLastCalledWith({ tag: '20261112' }))
  })

  it('a rejected release names the list and cannot be applied', async () => {
    vi.mocked(api.updateGeosite).mockResolvedValue(diff({ rejection: { reason: 'broad_entry', list: 'Porn', entry: 'regexp:.*' } }))
    open()
    fireEvent.click(await screen.findByRole('button', { name: '检查更新…' }))
    await screen.findByText(/^(更新规则数据？|回到上一版本？)$/)
    const dlg = (await screen.findAllByRole('dialog')).at(-1)!
    expect(within(dlg).getByText(/列表「Porn」里会出现过宽的条目/)).toBeTruthy()
    expect((within(dlg).getByRole('button', { name: '应用更新' }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('rolls back through a dry run and offers it only when a previous release exists', async () => {
    vi.mocked(api.rollbackGeosite).mockResolvedValue(diff({ to: meta('20261020') }))
    open()
    fireEvent.click(await screen.findByRole('button', { name: '回到上一版本（20261020）' }))
    await screen.findByText('回到上一版本？')
    expect(api.rollbackGeosite).toHaveBeenCalledWith(true)
  })

  it('hides rollback with no previous release and shows an unusable current data as an amber hint', async () => {
    vi.mocked(api.getGeositeStatus).mockResolvedValue(geo({ previous: undefined, data_error: 'geosite_data_corrupt' }))
    open()
    await screen.findByTestId('rule-data-panel')
    expect(screen.queryByRole('button', { name: /回到上一版本/ })).toBeNull()
    expect(screen.getByText('当前规则数据无法使用')).toBeTruthy()
  })

  it('shows the rejection of an apply-time 409 in words', async () => {
    vi.mocked(api.updateGeosite).mockResolvedValueOnce(diff()).mockRejectedValueOnce(Object.assign(new Error('x'), {
      isAxiosError: true,
      response: { status: 409, data: { code: 'dest_geosite_update_rejected', rejection: { reason: 'dest_list_too_large', list: 'Porn', used: 9, limit: 5 } } },
    }))
    open()
    fireEvent.click(await screen.findByRole('button', { name: '检查更新…' }))
    await screen.findByText('更新规则数据？')
    const dlg = (await screen.findAllByRole('dialog')).at(-1)!
    fireEvent.click(within(dlg).getByRole('button', { name: '应用更新' }))
    expect(await screen.findByText(/列表「Porn」会超出额度（9 \/ 5）/)).toBeTruthy()
  })

  it('a rollback apply aborts when the previous release changed after the review', async () => {
    vi.mocked(api.rollbackGeosite).mockResolvedValue(diff({ to: meta('20261020') }))
    open()
    fireEvent.click(await screen.findByRole('button', { name: '回到上一版本（20261020）' }))
    await screen.findByText('回到上一版本？')
    vi.mocked(api.getGeositeStatus).mockResolvedValue(geo({ previous: meta('20261005') }))
    const dlg = (await screen.findAllByRole('dialog')).at(-1)!
    fireEvent.click(within(dlg).getByRole('button', { name: '回到这个版本' }))
    expect(await screen.findByText(/上一版本已经变了/)).toBeTruthy()
    expect(api.rollbackGeosite).toHaveBeenCalledTimes(1)
  })

  it('a rollback apply aborts when the re-check read itself fails', async () => {
    vi.mocked(api.rollbackGeosite).mockResolvedValue(diff({ to: meta('20261020') }))
    open()
    fireEvent.click(await screen.findByRole('button', { name: '回到上一版本（20261020）' }))
    await screen.findByText('回到上一版本？')
    vi.mocked(api.getGeositeStatus).mockRejectedValue(new Error('down'))
    const dlg = (await screen.findAllByRole('dialog')).at(-1)!
    fireEvent.click(within(dlg).getByRole('button', { name: '回到这个版本' }))
    expect(await screen.findByText(/无法确认上一版本是否仍是刚才预览的那一版/)).toBeTruthy()
    expect(api.rollbackGeosite).toHaveBeenCalledTimes(1)
  })

  it('never prints a raw upstream or update error', async () => {
    vi.mocked(api.getGeositeStatus).mockResolvedValue(geo({
      latest: { tag: '', error: 'GET https://api.github.com: 403 secret-detail' }, last_update_error: 'sha256 mismatch for /data/x',
    }))
    open()
    await screen.findByTestId('rule-data-panel')
    expect(document.body.textContent).not.toMatch(/secret-detail|sha256 mismatch|api\.github/)
    expect(screen.getByText('检查上游失败')).toBeTruthy()
  })
})
