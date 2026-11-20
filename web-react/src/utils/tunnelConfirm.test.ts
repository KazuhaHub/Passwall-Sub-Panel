import { AxiosError } from 'axios'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const confirmMock = vi.hoisted(() => vi.fn())
vi.mock('@/components/ConfirmHost', () => ({ confirm: confirmMock }))
vi.mock('@/i18n', () => ({
  default: { t: (key: string, values?: Record<string, unknown>) => (values ? `${key} ${JSON.stringify(values)}` : key) },
}))
vi.mock('@/api/client', () => ({ client: {} }))

import { registerServerNames, tunnelConfirmMessage, withTunnelConfirm } from './tunnelConfirm'
import { frontErrorOf } from '@/api/fronts'

const refusal = (data: Record<string, unknown>, status = 409) => new AxiosError('x', String(status), undefined, undefined, {
  status, statusText: '', headers: {}, config: {} as never, data,
})

const tunnelRefusal = (params: Record<string, unknown>) => refusal({ error: 'e', code: 'confirm_required', params: { kind: 'tunnel', refresh_needed: true, ...params } })

beforeEach(() => vi.clearAllMocks())

describe('withTunnelConfirm', () => {
  it('passes a success through without asking', async () => {
    const run = vi.fn().mockResolvedValue('ok')
    expect(await withTunnelConfirm(run)).toBe('ok')
    expect(run).toHaveBeenCalledTimes(1)
    expect(confirmMock).not.toHaveBeenCalled()
  })

  it('names each listener and says subscribers must refresh, then repeats the change confirmed', async () => {
    confirmMock.mockResolvedValue(true)
    const run = vi.fn()
      .mockRejectedValueOnce(tunnelRefusal({ action: 'remove_line', removes: [
        { node_id: 9, name: 'hk-tunnel', line_name: 'Mobile', port: 40123 },
        { node_id: 10, name: 'sg-tunnel', line_name: 'Telecom', port: 40124, front_port: 443 },
      ] }))
      .mockResolvedValueOnce('saved')
    expect(await withTunnelConfirm(run)).toBe('saved')
    const asked = confirmMock.mock.calls[0][0]
    expect(asked.title).toBe('admin:nodes.tunnel.confirm.remove_line_title')
    expect(asked.message).toContain('"name":"hk-tunnel"')
    expect(asked.message).toContain('"port":40123')
    expect(asked.message).toContain('admin:nodes.tunnel.confirm.removes_item_front')
    expect(asked.message).toContain('"front_port":443')
    expect(asked.message).toContain('admin:nodes.tunnel.confirm.refresh')
    expect(run).toHaveBeenLastCalledWith({ plan: '' })
  })

  it('lists what a mode switch creates and removes', async () => {
    confirmMock.mockResolvedValue(true)
    const run = vi.fn()
      .mockRejectedValueOnce(tunnelRefusal({ action: 'switch_mode',
        creates: [{ line_id: 'L1', line_name: 'Mobile', entry_panel_id: 2, landing_name: 'HK-1' }], removes: [] }))
      .mockResolvedValueOnce('done')
    await withTunnelConfirm(run)
    expect(confirmMock.mock.calls[0][0].message).toContain('"landing":"HK-1"')
    expect(confirmMock.mock.calls[0][0].message).toContain('"line":"Mobile"')
  })

  it('throws the original refusal when the admin declines, and does not repeat the change', async () => {
    confirmMock.mockResolvedValue(false)
    const refused = tunnelRefusal({ action: 'delete_node', removes: [] })
    const run = vi.fn().mockRejectedValue(refused)
    await expect(withTunnelConfirm(run)).rejects.toBe(refused)
    expect(run).toHaveBeenCalledTimes(1)
  })

  it('leaves other refusals alone: a front confirmation, a plain 409, a 400', async () => {
    for (const err of [
      refusal({ code: 'confirm_required', params: { action: 'join', loopback_port: 30001 } }),
      refusal({ error: 'conflict' }),
      refusal({ error: 'port taken' }, 400),
    ]) {
      const run = vi.fn().mockRejectedValue(err)
      await expect(withTunnelConfirm(run)).rejects.toBe(err)
      expect(run).toHaveBeenCalledTimes(1)
    }
    expect(confirmMock).not.toHaveBeenCalled()
  })

  it('words a sync preview by its diff', () => {
    const e = frontErrorOf(tunnelRefusal({ action: 'sync_from_landing', diff: [{ field: 'flow', from: '', to: 'xtls-rprx-vision' }] }))!
    const text = tunnelConfirmMessage(e)
    expect(text).toContain('"field":"flow"')
    expect(text).toContain('"to":"xtls-rprx-vision"')
  })

  it('names every port with its server, and the tunnel port only when the tunnel is new', () => {
    registerServerNames([{ id: 2, name: 'entry-hk' }, { id: 3, name: 'exit-us' }])
    const created = tunnelConfirmMessage(frontErrorOf(tunnelRefusal({ action: 'switch_mode', creates: [
      { line_id: 'L1', line_name: 'Mobile', landing_name: 'US-1', entry_panel_id: 2, port: 40123, exit_panel_id: 3, link_port: 40500, link_new: true },
      { line_id: 'L2', line_name: 'Telecom', landing_name: 'US-1', entry_panel_id: 2, port: 40124, exit_panel_id: 3, link_port: 40500, link_new: false },
    ] }))!)
    expect(created).toContain('"port":40123,"entry":"entry-hk"')
    expect(created.match(/creates_link/g)).toHaveLength(1)
    expect(created).toContain('"port":40500,"exit":"exit-us","entry":"entry-hk"')
    const removed = tunnelConfirmMessage(frontErrorOf(tunnelRefusal({ action: 'remove_line', removes: [
      { node_id: 9, name: 'tn', line_name: 'Mobile', port: 40123, entry_panel_id: 2 }, { node_id: 10, name: 'tn2', port: 1, entry_panel_id: 99 },
    ] }))!)
    expect(removed).toContain('"entry":"entry-hk"')
    expect(removed).toContain('server_fallback {\\"id\\":99}')
  })

  it('words a line going back to a forward by its forward port only', () => {
    registerServerNames([{ id: 2, name: 'entry-hk' }])
    const text = tunnelConfirmMessage(frontErrorOf(tunnelRefusal({ action: 'switch_mode', creates: [
      { kind: 'forward', line_id: 'L1', line_name: 'Mobile', landing_name: 'US-1', entry_panel_id: 2, forward_port: 8443 },
    ], removes: [] }))!)
    expect(text).toContain('creates_forward {"line":"Mobile","landing":"US-1","port":8443,"entry":"entry-hk"}')
    expect(text).not.toContain('creates_item')
    expect(text).not.toContain('creates_link')
  })

  it('echoes the plan it showed on the confirmed resend', async () => {
    confirmMock.mockResolvedValue(true)
    const run = vi.fn().mockRejectedValueOnce(tunnelRefusal({ action: 'create_line', plan: 'plan-a', creates: [] })).mockResolvedValueOnce('made')
    expect(await withTunnelConfirm(run)).toBe('made')
    expect(confirmMock.mock.calls[0][0].title).toBe('admin:nodes.tunnel.confirm.create_line_title')
    expect(run).toHaveBeenNthCalledWith(1, undefined)
    expect(run).toHaveBeenLastCalledWith({ plan: 'plan-a' })
  })

  it('asks again with the fresh ports when the plan changed between asks, and stops when declined', async () => {
    confirmMock.mockResolvedValueOnce(true).mockResolvedValueOnce(false)
    const first = tunnelRefusal({ action: 'switch_mode', plan: 'p1', creates: [{ line_name: 'M', landing_name: 'L', entry_panel_id: 2, port: 40001 }] })
    const second = tunnelRefusal({ action: 'switch_mode', plan: 'p2', creates: [{ line_name: 'M', landing_name: 'L', entry_panel_id: 2, port: 40002 }] })
    const run = vi.fn().mockRejectedValueOnce(first).mockRejectedValueOnce(second)
    await expect(withTunnelConfirm(run)).rejects.toBe(second)
    expect(confirmMock).toHaveBeenCalledTimes(2)
    expect(confirmMock.mock.calls[0][0].message).toContain('"port":40001')
    expect(confirmMock.mock.calls[1][0].message).toContain('"port":40002')
    expect(run).toHaveBeenCalledTimes(2)
    expect(run).toHaveBeenLastCalledWith({ plan: 'p1' })
  })

  it('confirms a changed plan and resends with the newest one', async () => {
    confirmMock.mockResolvedValue(true)
    const run = vi.fn()
      .mockRejectedValueOnce(tunnelRefusal({ action: 'switch_mode', plan: 'p1', creates: [] }))
      .mockRejectedValueOnce(tunnelRefusal({ action: 'switch_mode', plan: 'p2', creates: [] }))
      .mockResolvedValueOnce('ok')
    expect(await withTunnelConfirm(run)).toBe('ok')
    expect(confirmMock).toHaveBeenCalledTimes(2)
    expect(run).toHaveBeenLastCalledWith({ plan: 'p2' })
  })

  it('does not ask again for the plan it already confirmed', async () => {
    confirmMock.mockResolvedValue(true)
    const same = tunnelRefusal({ action: 'remove_line', plan: 'p1', removes: [] })
    const run = vi.fn().mockRejectedValue(same)
    await expect(withTunnelConfirm(run)).rejects.toBe(same)
    expect(confirmMock).toHaveBeenCalledTimes(1)
    expect(run).toHaveBeenCalledTimes(2)
  })

  it('adds the caller\'s note to the question', async () => {
    confirmMock.mockResolvedValue(true)
    const run = vi.fn().mockRejectedValueOnce(tunnelRefusal({ action: 'sync_from_landing', plan: 'p', diff: [] })).mockResolvedValueOnce('ok')
    await withTunnelConfirm(run, 'unsaved edits go')
    expect(confirmMock.mock.calls[0][0].message).toContain('unsaved edits go')
    expect(confirmMock.mock.calls[0][0].message).toContain('sync_keeps')
  })

  describe('with a presenter', () => {
    it('hands the plan to the presenter instead of opening the shared confirmation', async () => {
      const present = vi.fn().mockResolvedValue(true)
      const run = vi.fn()
        .mockRejectedValueOnce(tunnelRefusal({ action: 'remove_line', plan: 'p1', removes: [{ node_id: 9, name: 'hk-tunnel', line_name: 'Mobile', port: 40123 }] }))
        .mockResolvedValueOnce('saved')
      expect(await withTunnelConfirm(run, 'a note', { present })).toBe('saved')
      expect(confirmMock).not.toHaveBeenCalled()
      expect(present).toHaveBeenCalledTimes(1)
      const shown = present.mock.calls[0][0]
      expect(shown.action).toBe('remove_line')
      expect(shown.title).toBe('admin:nodes.tunnel.confirm.remove_line_title')
      expect(shown.message).toContain('"name":"hk-tunnel"')
      expect(shown.message).toContain('a note')
      expect(shown.lines.some((l: string) => l.includes('"port":40123'))).toBe(true)
      expect(shown.lines.join('\n')).toBe(shown.message.replace('\na note', ''))
      expect(shown.error.params.plan).toBe('p1')
      expect(run).toHaveBeenLastCalledWith({ plan: 'p1' })
    })

    it('throws the refusal and does not resend when the presenter resolves false', async () => {
      const refused = tunnelRefusal({ action: 'delete_node', plan: 'p1', removes: [] })
      const present = vi.fn().mockResolvedValue(false)
      const run = vi.fn().mockRejectedValue(refused)
      await expect(withTunnelConfirm(run, undefined, { present })).rejects.toBe(refused)
      expect(run).toHaveBeenCalledTimes(1)
      expect(confirmMock).not.toHaveBeenCalled()
    })

    it('presents the fresh plan again when a second 409 follows a confirmation', async () => {
      const present = vi.fn().mockResolvedValue(true)
      const run = vi.fn()
        .mockRejectedValueOnce(tunnelRefusal({ action: 'switch_mode', plan: 'p1', creates: [{ line_name: 'M', landing_name: 'L', entry_panel_id: 2, port: 40001 }] }))
        .mockRejectedValueOnce(tunnelRefusal({ action: 'switch_mode', plan: 'p2', creates: [{ line_name: 'M', landing_name: 'L', entry_panel_id: 2, port: 40002 }] }))
        .mockResolvedValueOnce('done')
      expect(await withTunnelConfirm(run, undefined, { present })).toBe('done')
      expect(present).toHaveBeenCalledTimes(2)
      expect(present.mock.calls[0][0].message).toContain('"port":40001')
      expect(present.mock.calls[1][0].message).toContain('"port":40002')
      expect(run).toHaveBeenLastCalledWith({ plan: 'p2' })
    })

    it('stops when the second plan is declined', async () => {
      const second = tunnelRefusal({ action: 'switch_mode', plan: 'p2', creates: [] })
      const present = vi.fn().mockResolvedValueOnce(true).mockResolvedValueOnce(false)
      const run = vi.fn().mockRejectedValueOnce(tunnelRefusal({ action: 'switch_mode', plan: 'p1', creates: [] })).mockRejectedValueOnce(second)
      await expect(withTunnelConfirm(run, undefined, { present })).rejects.toBe(second)
      expect(run).toHaveBeenCalledTimes(2)
    })

    it('throws when the same plan is refused again after it was confirmed, without presenting it a second time', async () => {
      const again = tunnelRefusal({ action: 'switch_mode', plan: 'p1', creates: [] })
      const present = vi.fn().mockResolvedValue(true)
      const run = vi.fn().mockRejectedValue(again)
      await expect(withTunnelConfirm(run, undefined, { present })).rejects.toBe(again)
      expect(present).toHaveBeenCalledTimes(1)
    })

    it('leaves other refusals alone without calling the presenter', async () => {
      const err = refusal({ error: 'port taken' }, 400)
      const present = vi.fn()
      await expect(withTunnelConfirm(vi.fn().mockRejectedValue(err), undefined, { present })).rejects.toBe(err)
      expect(present).not.toHaveBeenCalled()
    })

    it('treats an abort while the presenter is pending as declined, without resending', async () => {
      const refused = tunnelRefusal({ action: 'delete_node', plan: 'p1', removes: [] })
      const ctl = new AbortController()
      let later: (v: boolean) => void = () => {}
      const present = vi.fn(() => new Promise<boolean>(r => { later = r }))
      const run = vi.fn().mockRejectedValue(refused)
      const pending = withTunnelConfirm(run, undefined, { present, signal: ctl.signal })
      await Promise.resolve(); await Promise.resolve()
      ctl.abort()
      await expect(pending).rejects.toBe(refused)
      later(true) // a late "yes" from the unmounted dialog changes nothing
      await Promise.resolve()
      expect(run).toHaveBeenCalledTimes(1)
    })

    it('does not leave an unhandled rejection when the presenter rejects after the abort', async () => {
      const refused = tunnelRefusal({ action: 'delete_node', plan: 'p1', removes: [] })
      const ctl = new AbortController()
      const unhandled = vi.fn()
      process.on('unhandledRejection', unhandled)
      let fail: (e: unknown) => void = () => {}
      const present = vi.fn(() => new Promise<boolean>((_, rej) => { fail = rej }))
      const pending = withTunnelConfirm(vi.fn().mockRejectedValue(refused), undefined, { present, signal: ctl.signal })
      await Promise.resolve(); await Promise.resolve()
      ctl.abort()
      await expect(pending).rejects.toBe(refused)
      fail(new Error('dialog gone'))
      await new Promise(r => setTimeout(r, 10))
      process.off('unhandledRejection', unhandled)
      expect(unhandled).not.toHaveBeenCalled()
    })

    it('does not present at all when the signal is already aborted', async () => {
      const refused = tunnelRefusal({ action: 'delete_node', plan: 'p1', removes: [] })
      const ctl = new AbortController(); ctl.abort()
      const present = vi.fn()
      await expect(withTunnelConfirm(vi.fn().mockRejectedValue(refused), undefined, { present, signal: ctl.signal })).rejects.toBe(refused)
      expect(present).not.toHaveBeenCalled()
    })

    it('hands the presenter the signal so it can close its dialog', async () => {
      const ctl = new AbortController()
      const present = vi.fn().mockResolvedValue(true)
      const run = vi.fn().mockRejectedValueOnce(tunnelRefusal({ action: 'switch_mode', plan: 'p1', creates: [] })).mockResolvedValueOnce('ok')
      await withTunnelConfirm(run, undefined, { present, signal: ctl.signal })
      expect(present.mock.calls[0][0].signal).toBe(ctl.signal)
    })

    it('lets a rejecting presenter surface its own error', async () => {
      const boom = new Error('dialog failed')
      const run = vi.fn().mockRejectedValue(tunnelRefusal({ action: 'switch_mode', plan: 'p1', creates: [] }))
      await expect(withTunnelConfirm(run, undefined, { present: vi.fn().mockRejectedValue(boom) })).rejects.toBe(boom)
    })
  })
})
