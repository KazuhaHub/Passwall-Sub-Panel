// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import * as api from '@/api/accessControl'
import type { DestBlockEvidence } from '@/api/riskSignals'
import { renderAccess, setTestLang, whereIs } from '@/test/accessControlHarness'
import { RiskKindEvidence } from './RiskEvidence'

vi.mock('@/api/accessControl', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/accessControl')>()
  const fns = Object.fromEntries(Object.keys(real).filter(k => typeof (real as Record<string, unknown>)[k] === 'function').map(k => [k, vi.fn()]))
  return { ...real, ...fns, destErrorOf: real.destErrorOf, isDestUnavailable: real.isDestUnavailable }
})

const ev = (over: Partial<DestBlockEvidence> = {}): DestBlockEvidence => ({
  v: 1, window_hours: 24, threshold: 20, total: 37,
  by_source: [{ source: 'p1', count: 30 }, { source: 'p2', count: 6 }, { source: 'p9', count: 1 }], nodes: 2, ...over,
})
const pol = (id: number, name: string) => ({ id, name, action: 'block' }) as unknown as api.DestPolicy
const policies = (...p: api.DestPolicy[]) => ({ allow: [], block: p, observe: [], exemptions: { count: 0 }, allowlist_groups: [], hit_window_days: 7, budget: {} }) as unknown as api.DestPolicies

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.getDestPolicies).mockResolvedValue(policies(pol(1, '禁止发信'), pol(2, '禁止 BT')))
})

const mount = (e: DestBlockEvidence = ev(), role: 'admin' | 'operator' = 'admin') =>
  renderAccess(<RiskKindEvidence kind="dest_block" evidence={e} userId={7} />, { role })

describe('the dest_block evidence (S19)', () => {
  it('lists the blocks by policy by name, and the nodes that record: no address, port or destination', async () => {
    mount()
    expect(await screen.findByText('禁止发信')).toBeTruthy()
    const panel = screen.getByTestId('dest-block-evidence')
    expect(panel.textContent).toContain('禁止发信30')
    expect(panel.textContent).toContain('禁止 BT6')
    expect(panel.textContent).toContain('2 台记录命中的节点')
    // A policy that is gone is named as such.
    expect(panel.textContent).toContain('已删除的策略1')
    // Raw codes never reach the page.
    expect(panel.textContent).not.toMatch(/\bp\d+\b|\bs\d+\b/)
  })

  it('a source rule is named a source rule, never by its id', async () => {
    mount(ev({ by_source: [{ source: 's4', count: 9 }] }))
    expect(await screen.findByText('来源规则')).toBeTruthy()
    expect(screen.getByTestId('dest-block-evidence').textContent).not.toContain('s4')
  })

  it('"view these hits" goes to the records of this account over the window, blocks only', async () => {
    const { router } = mount()
    fireEvent.click(await screen.findByRole('button', { name: '查看这些命中' }))
    await waitFor(() => expect(whereIs(router)).toBe('/admin/access-control?tab=records&rec_user=7&rec_action=block'))
  })

  it('an operator cannot read the policies nor open the page: no names, no link, still no raw code', async () => {
    mount(ev(), 'operator')
    const panel = await screen.findByTestId('dest-block-evidence')
    expect(api.getDestPolicies).not.toHaveBeenCalled()
    expect(screen.queryByRole('button', { name: '查看这些命中' })).toBeNull()
    expect(panel.textContent).toContain('策略 #1')
    expect(panel.textContent).not.toMatch(/\bp\d+\b/)
  })

  it('a verdict with nothing to show draws nothing', () => {
    renderAccess(<RiskKindEvidence kind="dest_block" evidence={null} userId={7} />)
    expect(screen.queryByTestId('dest-block-evidence')).toBeNull()
  })

  it('in English', async () => {
    setTestLang('en-US')
    mount()
    expect(await screen.findByText('2 nodes recording hits')).toBeTruthy()
    expect(screen.getByRole('button', { name: 'View these hits' })).toBeTruthy()
  })
})
