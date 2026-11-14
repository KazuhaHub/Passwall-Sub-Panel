// @vitest-environment jsdom
import { fireEvent, screen, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import type { DestPolicy } from '@/api/accessControl'
import { renderAccess } from '@/test/accessControlHarness'
import PolicyRow from './PolicyRow'

const media = vi.hoisted(() => ({ phone: true }))
vi.mock('@mui/material', async importOriginal => ({ ...await importOriginal<typeof import('@mui/material')>(), useMediaQuery: () => media.phone }))

const policy = (over: Partial<DestPolicy> = {}): DestPolicy => ({
  id: 3, name: '加密货币交易所', description: '', action: 'block', state: 'report', list_ids: [], inline: { ports: '443' }, scope: 'all', group_ids: [], panel_ids: [],
  priority: 1, template_key: '', created_at: 0, updated_at: 1, list_states: [], note: null, scope_missing: false,
  hits_recent: 0, would_block_recent: 1032, last_hit_at: Date.now() - 60_000, ...over,
})
const handlers = { onOpen: vi.fn(), onToggle: vi.fn(), onMove: vi.fn(), onDuplicate: vi.fn(), onDelete: vi.fn(), onReport: vi.fn(), onConvert: vi.fn(), onViewRecords: vi.fn() }
const row = (p: DestPolicy) => renderAccess(
  <PolicyRow policy={p} index={0} count={1} listNames={{}} scopeText="所有人" busy={false} moving={false} highlighted={false} hitDays={7} {...handlers} />,
)

describe('a policy row on a phone', () => {
  it('moves "turn into a block" from the first line to the hits line, so the name keeps its width, and it is still one button', () => {
    media.phone = true
    row(policy())
    const r = screen.getByTestId('policy-row')
    expect(within(r).getAllByRole('button', { name: '把「加密货币交易所」转为拦截' })).toHaveLength(1)
    const hits = within(r).getByTestId('policy-hits').parentElement!
    expect(within(hits).getByRole('button', { name: '把「加密货币交易所」转为拦截' })).toBeTruthy()
    fireEvent.click(within(hits).getByRole('button', { name: '把「加密货币交易所」转为拦截' }))
    expect(handlers.onConvert).toHaveBeenCalled()
    expect(handlers.onOpen).not.toHaveBeenCalled()
  })

  it('on a wide screen it sits beside the switch instead', () => {
    media.phone = false
    row(policy())
    const r = screen.getByTestId('policy-row')
    const hits = within(r).getByTestId('policy-hits').parentElement!
    expect(within(hits).queryByRole('button', { name: '把「加密货币交易所」转为拦截' })).toBeNull()
    expect(within(r).getAllByRole('button', { name: '把「加密货币交易所」转为拦截' })).toHaveLength(1)
  })

  it('an enforced policy has no convert button on either', () => {
    media.phone = true
    row(policy({ state: 'on', hits_recent: 214 }))
    expect(screen.queryByRole('button', { name: /转为拦截/ })).toBeNull()
  })

  it('every target is at least 44px: the row menu button', () => {
    media.phone = true
    row(policy())
    const menu = screen.getByRole('button', { name: '「加密货币交易所」的更多操作' })
    expect(getComputedStyle(menu).width).toBe('44px')
    expect(getComputedStyle(menu).height).toBe('44px')
  })
})

describe('relative times', () => {
  it('are measured from the `now` the list was read at, not from each render', () => {
    media.phone = false
    const now = 1_800_000_000_000
    renderAccess(
      <PolicyRow policy={policy({ state: 'on', hits_recent: 5, would_block_recent: null, last_hit_at: now - 3 * 3_600_000 })} index={0} count={1} listNames={{}} scopeText="所有人"
        busy={false} moving={false} highlighted={false} hitDays={7} now={now} {...handlers} />,
    )
    expect(within(screen.getByTestId('policy-hits')).getByRole('button').textContent).toContain('3 小时前')
  })
})
