// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Node, ProbeQuality } from '@/api/types'
import { api, installReads, list, mountPage } from '../testHarness'
import { landing } from '../lines/editorHarness'
import { useNodeDetail } from '../lines/useNodeDetail'
import { ExitTab } from './ExitTab'

const toast = vi.hoisted(() => vi.fn())
vi.mock('@/components/feedback/toasts', () => ({ pushToast: toast, pushSnack: toast }))

const psp = (id: number, name: string) => ({ id, name, url: `https://s${id}.example`, panel_type: 'psp', capabilities: ['inbound.enable'] })
const servers = [psp(1, 'Landing server'), psp(2, 'Osaka'), psp(3, 'Tokyo')]
const link = { exists: true, protocol: 'ss2022', exit_address: '', reality_server_name: '', reality_target: '', port: 40000, server_address: '203.0.113.2', up_bytes: 0, down_bytes: 0 }
const routes = { supported: true, routes: [], default_exit: { kind: 'local' }, sent: false, running: false, warnings: [] }

function open(over: Partial<Node> = {}, svs: unknown[] = servers, live?: () => Node) {
  const node = landing([], over)
  const reads = {
    '/admin/nodes': list([node]), '/admin/servers': list(svs), '/admin/nodes/1/routes': routes, '/admin/dest/lists': { items: [], refresh_hours: 24, budget: {} },
    '/admin/transit/links/1/2': link, '/admin/transit/links/1/3': link, '/admin/transit/probes': { items: [] },
  }
  installReads({ ...reads, '/admin/nodes/1': { node, clients: [] } })
  if (live) {
    const base = api.get.getMockImplementation()!
    api.get.mockImplementation(async (url: string, ...rest: unknown[]) => (url === '/admin/nodes/1' ? { data: { node: live(), clients: [] } } : base(url, ...rest)))
  }
  const onDirtyChange = vi.fn()
  mountPage(live ? <Live onDirtyChange={onDirtyChange} /> : <ExitTab node={node} onDirtyChange={onDirtyChange} />)
  return { node, onDirtyChange }
}
// The page hands the tab the node it reads, which the tab's own answers update in the cache.
function Live({ onDirtyChange }: { onDirtyChange: (d: boolean) => void }) {
  const q = useNodeDetail(1)
  return q.data ? <ExitTab node={q.data.node} onDirtyChange={onDirtyChange} /> : null
}
// The exit server is a searchable combobox: type to filter, then choose the option.
async function chooseExit(name: string) {
  const input = await screen.findByRole('combobox', { name: /Exit server/ })
  fireEvent.mouseDown(input)
  fireEvent.click(await screen.findByRole('option', { name }))
}
const puts = () => api.put.mock.calls.map(([url, body]) => [url, body] as const)

beforeEach(() => { toast.mockReset(); api.put.mockImplementation(async (_url: string, body: object) => ({ data: { ...landing([]), ...body } })) })

describe('Exit tab: the default exit', () => {
  it('saves the tunnel settings first, then the exit and the current flow, and nothing else', async () => {
    open()
    fireEvent.click(await screen.findByRole('radio', { name: /Leave through another server/ }))
    await chooseExit('Osaka')
    // Tunnel settings are a collapsed section; its fields are mounted and carry the edit.
    fireEvent.change(await screen.findByLabelText('Exit address'), { target: { value: ' 198.51.100.7 ' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(puts().filter(([url]) => url !== '/admin/nodes/1/routes')).toHaveLength(2))
    const sent = puts()
    expect(sent[0][0]).toBe('/admin/transit/links/1/2')
    expect(sent[0][1]).toMatchObject({ protocol: 'ss2022', exit_address: '198.51.100.7' })
    // No `relays`, no `sort_order`: the lines and the node's position are not this tab's.
    expect(sent[1]).toEqual(['/admin/nodes/1/metadata', { egress_panel_id: 2, flow: 'xtls-rprx-vision' }])
  })

  it('says which part is already saved when the exit fails after the tunnel settings were written', async () => {
    open()
    fireEvent.click(await screen.findByRole('radio', { name: /Leave through another server/ }))
    await chooseExit('Osaka')
    fireEvent.change(await screen.findByLabelText('Exit address'), { target: { value: '198.51.100.7' } })
    api.put.mockImplementation(async (url: string, body: object) => {
      if (url === '/admin/nodes/1/metadata') throw Object.assign(new Error('x'), { response: { data: { error: 'exit refused' } } })
      return { data: { ...link, ...body } }
    })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect((await screen.findByText(/Tunnel settings were saved, but the exit server was not: exit refused/)).textContent).toBeTruthy()
    // A retry sends only what is left.
    api.put.mockClear()
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(puts().filter(([u]) => u === '/admin/nodes/1/metadata')).toHaveLength(1))
    expect(puts().some(([u]) => u === '/admin/transit/links/1/2')).toBe(false)
  })

  it('keeps the bar and the message when only the tunnel settings were edited and the exit write then fails', async () => {
    open({ egress_panel_id: 2 })
    fireEvent.change(await screen.findByLabelText('Exit address'), { target: { value: '198.51.100.7' } })
    api.put.mockImplementation(async (url: string, body: object) => {
      if (url === '/admin/nodes/1/metadata') throw Object.assign(new Error('x'), { response: { data: { error: 'exit refused' } } })
      return { data: { ...link, ...body } }
    })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText(/Tunnel settings were saved, but the exit server was not: exit refused/)).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Save' })).toBeTruthy()
  })

  it('asks for a server before it sends anything when "another server" has none picked', async () => {
    open()
    fireEvent.click(await screen.findByRole('radio', { name: /Leave through another server/ }))
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findAllByText(/Choose the exit server/)).not.toHaveLength(0)
    expect(api.put).not.toHaveBeenCalled()
  })

  it('greys out both cards and says why once, in the banner, when the node is not on a Passwall Node server', async () => {
    open({}, [{ ...servers[0], panel_type: '3xui' }, servers[1]])
    const card = await screen.findByRole('radio', { name: /Leave through another server/ })
    expect(card.getAttribute('aria-disabled')).toBe('true')
    expect(screen.getAllByText(/not a Passwall Node server/)).toHaveLength(1)
  })

  it('reports unsaved edits, including a pending choice with no server yet', async () => {
    const { onDirtyChange } = open()
    await screen.findByRole('radio', { name: /Leave from this server/ })
    expect(onDirtyChange).toHaveBeenLastCalledWith(false)
    fireEvent.click(screen.getByRole('radio', { name: /Leave through another server/ }))
    await waitFor(() => expect(onDirtyChange).toHaveBeenLastCalledWith(true))
    fireEvent.click(screen.getByRole('radio', { name: /Leave from this server/ }))
    await waitFor(() => expect(onDirtyChange).toHaveBeenLastCalledWith(false))
  })
})

describe('Exit tab: waiting for the tunnel', () => {
  beforeEach(() => { vi.useFakeTimers({ shouldAdvanceTime: true }) })
  afterEach(() => { vi.useRealTimers() })

  it('turns "waiting" into "ready" once the tunnel runs on both servers, and then stops asking', async () => {
    let ready = false
    open({ egress_panel_id: 2 }, servers, () => ({ ...landing([]), egress_panel_id: 2, egress_ready: ready }))
    expect((await screen.findByTestId('egress-status')).textContent).toMatch(/Waiting for the tunnel/)
    ready = true
    await act(async () => { await vi.advanceTimersByTimeAsync(5000) })
    await waitFor(() => expect(screen.getByTestId('egress-status').textContent).toMatch(/node is published/))
    const asked = api.get.mock.calls.filter(([u]) => u === '/admin/nodes/1').length
    await act(async () => { await vi.advanceTimersByTimeAsync(15000) })
    expect(api.get.mock.calls.filter(([u]) => u === '/admin/nodes/1').length).toBe(asked)
  })

  it('shows the link quality next to the status, and says it is withheld', async () => {
    const probe: ProbeQuality = { state: 'degraded', median_rtt_ms: 80, loss_percent: 5, withheld: false }
    open({ egress_panel_id: 2, egress_ready: true, egress_probe: probe, egress_withheld: true })
    const chip = await screen.findByTestId('egress-probe')
    expect(chip.dataset.state).toBe('degraded')
    expect(chip.textContent).toMatch(/Withheld/)
  })
})
