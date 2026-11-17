import { AxiosError } from 'axios'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const http = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn(), delete: vi.fn() }))
vi.mock('./client', () => ({ client: http }))
const confirmMock = vi.hoisted(() => vi.fn())
vi.mock('@/components/ConfirmHost', () => ({ confirm: confirmMock }))
vi.mock('@/i18n', () => ({ default: { t: (key: string) => key } }))

import * as api from './nodes'

const refusal = (data: Record<string, unknown>, status = 409) => new AxiosError('x', String(status), undefined, undefined, {
  status, statusText: '', headers: {}, config: {} as never, data,
})
const tunnelRefusal = (action: string) => refusal({ code: 'confirm_required', params: { kind: 'tunnel', action, removes: [], creates: [], refresh_needed: true } })

beforeEach(() => vi.clearAllMocks())

describe('tunnel line api', () => {
  it('switches a line and sends the confirmation only after the admin agreed', async () => {
    confirmMock.mockResolvedValue(true)
    http.post.mockRejectedValueOnce(tunnelRefusal('switch_mode')).mockResolvedValueOnce({ data: { id: 1, relays: [] } })
    await api.switchLineMode(1, 'L1', 'tunnel')
    expect(http.post).toHaveBeenNthCalledWith(1, '/admin/nodes/1/lines/L1/mode', { mode: 'tunnel' }, expect.objectContaining({ _skipErrorToast: true }))
    expect(http.post).toHaveBeenNthCalledWith(2, '/admin/nodes/1/lines/L1/mode', { mode: 'tunnel', confirm: true }, expect.anything())
    expect(confirmMock).toHaveBeenCalledTimes(1)
  })

  it('changes nothing when the switch is declined', async () => {
    confirmMock.mockResolvedValue(false)
    http.post.mockRejectedValue(tunnelRefusal('switch_mode'))
    await expect(api.switchLineMode(1, 'L1', 'forward', 8443)).rejects.toBeTruthy()
    expect(http.post).toHaveBeenCalledTimes(1)
    expect(http.post.mock.calls[0][1]).toEqual({ mode: 'forward', port: 8443 })
  })

  it('edits the listener, previews and applies the sync', async () => {
    http.put.mockResolvedValue({ data: { relays: [{ id: 'L1' }] } })
    const input = { port: 40100, server_name: 'a.example', target: 'a.example:443', short_id: 'ab', private_key: '', public_key: '', fingerprint: '' }
    expect(await api.updateTunnelListener(1, 'L1', input)).toEqual([{ id: 'L1' }])
    expect(http.put).toHaveBeenCalledWith('/admin/nodes/1/lines/L1/tunnel', input, expect.objectContaining({ _skipErrorToast: true }))
    http.get.mockResolvedValue({ data: { diff: [{ field: 'flow', from: '', to: 'v' }] } })
    expect(await api.previewTunnelSync(1, 'L1')).toEqual([{ field: 'flow', from: '', to: 'v' }])
    http.get.mockResolvedValue({ data: { diff: null } })
    expect(await api.previewTunnelSync(1, 'L1')).toEqual([])
    confirmMock.mockResolvedValue(true)
    http.post.mockRejectedValueOnce(tunnelRefusal('sync_from_landing')).mockResolvedValueOnce({ data: { diff: [], relays: [{ id: 'L1' }] } })
    expect(await api.syncTunnelFromLanding(1, 'L1')).toEqual([{ id: 'L1' }])
    expect(http.post).toHaveBeenNthCalledWith(1, '/admin/nodes/1/lines/L1/tunnel/sync', {}, expect.anything())
    expect(http.post).toHaveBeenLastCalledWith('/admin/nodes/1/lines/L1/tunnel/sync', { confirm: true }, expect.anything())
  })

  it('asks before a save removes a tunnel line, then repeats it with confirm_tunnels', async () => {
    confirmMock.mockResolvedValue(true)
    http.put.mockRejectedValueOnce(tunnelRefusal('remove_line')).mockResolvedValueOnce({ data: { id: 1 } })
    await api.updateNodeMetadata(1, { display_name: 'n' })
    expect(http.put).toHaveBeenNthCalledWith(1, '/admin/nodes/1/metadata', { display_name: 'n' })
    expect(http.put).toHaveBeenNthCalledWith(2, '/admin/nodes/1/metadata', { display_name: 'n' }, { params: { confirm_tunnels: 'true' } })
  })

  it('leaves a declined save unsent', async () => {
    confirmMock.mockResolvedValue(false)
    http.put.mockRejectedValue(tunnelRefusal('remove_line'))
    await expect(api.updateNodeMetadata(1, { display_name: 'n' })).rejects.toBeTruthy()
    expect(http.put).toHaveBeenCalledTimes(1)
  })

  it('asks before deleting or detaching a landing with tunnel listeners', async () => {
    confirmMock.mockResolvedValue(true)
    http.delete.mockRejectedValueOnce(tunnelRefusal('delete_node')).mockResolvedValueOnce({ data: {} })
    await api.deleteNode(3)
    expect(http.delete).toHaveBeenNthCalledWith(1, '/admin/nodes/3', undefined)
    expect(http.delete).toHaveBeenNthCalledWith(2, '/admin/nodes/3', { params: { confirm_tunnels: 'true' } })
    http.post.mockRejectedValueOnce(tunnelRefusal('delete_node')).mockResolvedValueOnce({ data: {} })
    await api.detachNode(3)
    expect(http.post).toHaveBeenLastCalledWith('/admin/nodes/3/detach', undefined, { params: { confirm_tunnels: 'true' } })
  })

  it('still confirms a shared port the old way', async () => {
    confirmMock.mockResolvedValue(true)
    http.delete.mockRejectedValueOnce(refusal({ code: 'confirm_required', params: { action: 'leave', port: 443 } })).mockResolvedValueOnce({ data: {} })
    await api.deleteNode(3)
    expect(http.delete).toHaveBeenLastCalledWith('/admin/nodes/3', { params: { confirm_front: 'true' } })
  })

  it('derives the public key of a private key quietly', async () => {
    http.post.mockResolvedValue({ data: { public_key: 'PUB' } })
    expect(await api.deriveRealityPublicKey('PRIV')).toBe('PUB')
    expect(http.post).toHaveBeenCalledWith('/admin/nodes/reality-public-key', { private_key: 'PRIV' }, expect.objectContaining({ _skipErrorToast: true }))
  })

  const frontRefusal = () => refusal({ code: 'confirm_required', params: { action: 'join', loopback_port: 30044, port: 443 } })

  it('asks about a shared port on a mode switch and resends with confirm_front in the body', async () => {
    confirmMock.mockResolvedValue(true)
    http.post.mockRejectedValueOnce(frontRefusal()).mockResolvedValueOnce({ data: { id: 1 } })
    await api.switchLineMode(1, 'L1', 'tunnel')
    expect(http.post).toHaveBeenLastCalledWith('/admin/nodes/1/lines/L1/mode', { mode: 'tunnel', confirm_front: true }, expect.anything())
  })

  it('keeps both confirmations when a tunnel one is followed by a shared-port one', async () => {
    confirmMock.mockResolvedValue(true)
    http.post.mockRejectedValueOnce(tunnelRefusal('switch_mode')).mockRejectedValueOnce(frontRefusal()).mockResolvedValueOnce({ data: { id: 1 } })
    await api.switchLineMode(1, 'L1', 'forward', 8443)
    expect(confirmMock).toHaveBeenCalledTimes(2)
    expect(http.post).toHaveBeenLastCalledWith('/admin/nodes/1/lines/L1/mode', { mode: 'forward', port: 8443, confirm: true, confirm_front: true }, expect.anything())
  })

  it('leaves a declined shared-port switch unsent', async () => {
    confirmMock.mockResolvedValue(false)
    http.post.mockRejectedValue(frontRefusal())
    await expect(api.switchLineMode(1, 'L1', 'tunnel')).rejects.toBeTruthy()
    expect(http.post).toHaveBeenCalledTimes(1)
  })

  it('resends a sync and a listener edit with the shared-port go-ahead', async () => {
    confirmMock.mockResolvedValue(true)
    http.post.mockRejectedValueOnce(frontRefusal()).mockResolvedValueOnce({ data: { diff: [], relays: [] } })
    await api.syncTunnelFromLanding(1, 'L1')
    expect(http.post).toHaveBeenLastCalledWith('/admin/nodes/1/lines/L1/tunnel/sync', { confirm_front: true }, expect.anything())
    http.put.mockRejectedValueOnce(frontRefusal()).mockResolvedValueOnce({ data: { relays: [] } })
    const input = { port: 443, server_name: '', target: '', short_id: '', private_key: '', public_key: '', fingerprint: '' }
    await api.updateTunnelListener(1, 'L1', input)
    expect(http.put).toHaveBeenLastCalledWith('/admin/nodes/1/lines/L1/tunnel', input, expect.objectContaining({ params: { confirm_front: 'true' } }))
  })

  it('resends a metadata save with confirm_front after a shared-port confirmation', async () => {
    confirmMock.mockResolvedValue(true)
    http.put.mockRejectedValueOnce(frontRefusal()).mockResolvedValueOnce({ data: { id: 1 } })
    await api.updateNodeMetadata(1, { display_name: 'n' })
    expect(http.put).toHaveBeenLastCalledWith('/admin/nodes/1/metadata', { display_name: 'n' }, { params: { confirm_front: 'true' } })
  })

  const planned = (action: string, plan: string) => refusal({ code: 'confirm_required', params: { kind: 'tunnel', action, plan, creates: [], removes: [], refresh_needed: true } })

  it('echoes the plan in the body of a mode switch and a sync', async () => {
    confirmMock.mockResolvedValue(true)
    http.post.mockRejectedValueOnce(planned('switch_mode', 'P1')).mockResolvedValueOnce({ data: { id: 1 } })
    await api.switchLineMode(1, 'L1', 'tunnel')
    expect(http.post).toHaveBeenLastCalledWith('/admin/nodes/1/lines/L1/mode', { mode: 'tunnel', confirm: true, confirm_plan: 'P1' }, expect.anything())
    http.post.mockRejectedValueOnce(planned('sync_from_landing', 'P2')).mockResolvedValueOnce({ data: { diff: [], relays: [] } })
    await api.syncTunnelFromLanding(1, 'L1')
    expect(http.post).toHaveBeenLastCalledWith('/admin/nodes/1/lines/L1/tunnel/sync', { confirm: true, confirm_plan: 'P2' }, expect.anything())
  })

  it('echoes the plan in the query of a save, a delete and a detach', async () => {
    confirmMock.mockResolvedValue(true)
    http.put.mockRejectedValueOnce(planned('create_line', 'a b/1')).mockResolvedValueOnce({ data: { id: 1 } })
    await api.updateNodeMetadata(1, { display_name: 'n' })
    expect(http.put).toHaveBeenLastCalledWith('/admin/nodes/1/metadata', { display_name: 'n' }, { params: { confirm_tunnels: 'true', confirm_plan: 'a b/1' } })
    http.delete.mockRejectedValueOnce(planned('delete_node', 'P3')).mockResolvedValueOnce({ data: {} })
    await api.deleteNode(3)
    expect(http.delete).toHaveBeenLastCalledWith('/admin/nodes/3', { params: { confirm_tunnels: 'true', confirm_plan: 'P3' } })
    http.post.mockRejectedValueOnce(planned('delete_node', 'P4')).mockResolvedValueOnce({ data: {} })
    await api.detachNode(3)
    expect(http.post).toHaveBeenLastCalledWith('/admin/nodes/3/detach', undefined, { params: { confirm_tunnels: 'true', confirm_plan: 'P4' } })
  })

  it('asks again when the plan changes between the asks', async () => {
    confirmMock.mockResolvedValue(true)
    http.put.mockRejectedValueOnce(planned('create_line', 'P1')).mockRejectedValueOnce(planned('create_line', 'P2')).mockResolvedValueOnce({ data: { id: 1 } })
    await api.updateNodeMetadata(1, { display_name: 'n' })
    expect(confirmMock).toHaveBeenCalledTimes(2)
    expect(http.put).toHaveBeenLastCalledWith('/admin/nodes/1/metadata', { display_name: 'n' }, { params: { confirm_tunnels: 'true', confirm_plan: 'P2' } })
  })
})
