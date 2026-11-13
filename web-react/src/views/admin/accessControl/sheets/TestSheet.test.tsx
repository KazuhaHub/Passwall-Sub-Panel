// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { renderAccess, whereIs } from '@/test/accessControlHarness'
import * as api from '@/api/accessControl'
import TestSheet from './TestSheet'

vi.mock('@/api/accessControl', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/accessControl')>()
  const fns = Object.fromEntries(Object.keys(real).filter(k => typeof (real as Record<string, unknown>)[k] === 'function').map(k => [k, vi.fn()]))
  return { ...real, ...fns, destErrorOf: real.destErrorOf, isDestUnavailable: real.isDestUnavailable }
})
vi.mock('@/api/nodes', async importOriginal => ({ ...await importOriginal<typeof import('@/api/nodes')>(), listNodes: vi.fn().mockResolvedValue({ items: [{ id: 4, panel_name: 'Tokyo', display_name: 'tokyo-vless' }], total: 1, page: 1, page_size: 200 }) }))
vi.mock('@/components/UserAutocomplete', () => ({ default: () => <input aria-label="account" /> }))

const blocked = (over: Partial<api.DestTestResult> = {}): api.DestTestResult => ({
  verdict: 'block', terminating_step: 'p1', conditional: false, unpublished: false, notes: [], nodes: [], builtin: undefined,
  steps: [
    { step: 1, kind: 'allow', policy_id: 9, name: 'Internal', result: 'miss' },
    { step: 2, kind: 'exempt', result: 'miss' },
    { step: 3, kind: 'block', policy_id: 1, name: 'No mail', result: 'hit', entry: '587' },
    { step: 4, kind: 'block', policy_id: 2, name: 'Crypto', result: 'shadowed', entry: 'binance.com' },
  ], ...over,
})

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.getDestStatus).mockResolvedValue({ generation: 1, published_generation: 1, nodes: [], policy_notes: {}, totals: {}, apply_eta_ms: 0 } as unknown as api.DestStatus)
})

const run = () => fireEvent.click(screen.getByRole('button', { name: '测试' }))

describe('TestSheet', () => {
  it('starts with an instruction, tests on submit and shows the verdict, the trace and the shadowed hit', async () => {
    vi.mocked(api.testDestination).mockResolvedValue(blocked())
    renderAccess(<TestSheet open onClose={() => {}} onOpenPolicy={() => {}} />)
    expect(screen.getByText('输入目的地，看它在哪一步被放行或拦截')).toBeTruthy()
    fireEvent.change(screen.getByRole('textbox', { name: /目的地/ }), { target: { value: 'smtp.gmail.com' } })
    fireEvent.change(screen.getByRole('textbox', { name: '端口' }), { target: { value: '587' } })
    run()
    expect(await screen.findByText('在第 3 步被「No mail」拦截（587）')).toBeTruthy()
    expect(api.testDestination).toHaveBeenCalledWith(expect.objectContaining({ target: 'smtp.gmail.com', port: 587, network: 'tcp' }))
    const trace = screen.getByTestId('eval-trace')
    expect(within(trace).getByText('也会命中（被前面截住）')).toBeTruthy()
    expect(within(trace).getAllByRole('listitem')).toHaveLength(4)
  })

  it('a pasted URL is cut down to its host with the hint, and the prefill never touches the URL', async () => {
    vi.mocked(api.testDestination).mockResolvedValue(blocked({ verdict: 'default', terminating_step: '', steps: [] }))
    const { router } = renderAccess(<TestSheet open onClose={() => {}} onOpenPolicy={() => {}} prefill={{ target: 'https://example.com/a?b=1' }} />, { route: '/admin/access-control?sheet=test' })
    expect(await screen.findByText('只看主机名，看不到网址路径')).toBeTruthy()
    run()
    await waitFor(() => expect(api.testDestination).toHaveBeenCalledWith(expect.objectContaining({ target: 'example.com' })))
    expect(await screen.findByText('没有规则命中，直接放行')).toBeTruthy()
    expect(whereIs(router)).toBe('/admin/access-control?sheet=test')
  })

  it('validates the port and the source IP before asking', async () => {
    renderAccess(<TestSheet open onClose={() => {}} onOpenPolicy={() => {}} />)
    fireEvent.change(screen.getByRole('textbox', { name: /目的地/ }), { target: { value: 'a.com' } })
    fireEvent.change(screen.getByRole('textbox', { name: '端口' }), { target: { value: '99999' } })
    fireEvent.change(screen.getByRole('textbox', { name: /来源 IP/ }), { target: { value: 'nope' } })
    run()
    expect(screen.getByText('端口是 1–65535')).toBeTruthy()
    expect(screen.getByText('不是有效的 IP 地址')).toBeTruthy()
    expect(api.testDestination).not.toHaveBeenCalled()
  })

  it('unpublished changes get an amber line with Apply now; a policy step opens its policy', async () => {
    vi.mocked(api.testDestination).mockResolvedValue(blocked({ unpublished: true }))
    vi.mocked(api.publishDest).mockResolvedValue({ published_generation: 2, generation: 2, unpublished: false })
    const onOpenPolicy = vi.fn()
    renderAccess(<TestSheet open onClose={() => {}} onOpenPolicy={onOpenPolicy} />)
    fireEvent.change(screen.getByRole('textbox', { name: /目的地/ }), { target: { value: 'a.com' } })
    run()
    expect(await screen.findByText('有改动尚未下发，结果按已下发的版本判断')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '立即下发' }))
    await waitFor(() => expect(api.publishDest).toHaveBeenCalled())
    fireEvent.click(screen.getByRole('button', { name: '打开「No mail」' }))
    expect(onOpenPolicy).toHaveBeenCalledWith(1)
  })

  it('shows the node-internal trailing-dot step and the hostname note', async () => {
    vi.mocked(api.testDestination).mockResolvedValue({
      verdict: 'block', terminating_step: 'trailing_dot', builtin: 'trailing_dot', conditional: false, unpublished: false, nodes: [], notes: ['hostname_skips_ip_rules'],
      steps: [{ step: 1, kind: 'builtin', builtin: 'trailing_dot', result: 'hit' }],
    })
    renderAccess(<TestSheet open onClose={() => {}} onOpenPolicy={() => {}} />)
    fireEvent.change(screen.getByRole('textbox', { name: /目的地/ }), { target: { value: 'example.com.' } })
    run()
    expect(await screen.findByText('被节点内置规则拦截：以 . 结尾的主机名')).toBeTruthy()
    expect(screen.getByText('节点内置：拦截以 . 结尾的主机名')).toBeTruthy()
    expect(screen.getByText(/这是主机名/)).toBeTruthy()
  })

  it('adds a global allow exception for the tested host', async () => {
    vi.mocked(api.testDestination).mockResolvedValue(blocked())
    vi.mocked(api.addDestException).mockResolvedValue({ entry: 'smtp.gmail.com', created: null })
    renderAccess(<TestSheet open onClose={() => {}} onOpenPolicy={() => {}} />)
    fireEvent.change(screen.getByRole('textbox', { name: /目的地/ }), { target: { value: 'smtp.gmail.com' } })
    run()
    fireEvent.click(await screen.findByRole('button', { name: '加入放行例外…' }))
    fireEvent.click(await screen.findByRole('button', { name: '加入' }))
    await waitFor(() => expect(api.addDestException).toHaveBeenCalledWith({ target: 'smtp.gmail.com', match: 'site' }))
  })
})
