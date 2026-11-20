// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { OrderRow } from '@/api/nodes'
import i18n from '@/test/initI18n'
import { flatten, type Nested } from '@/i18n/options'
import enAdmin from '@/locales/en-US/admin.json'
import { TopologyView } from './TopologyView'

i18n.addResourceBundle('en-US', 'admin', flatten(enAdmin as Nested), true, true)
afterEach(cleanup)

const landing = (id: number) => ({ node_id: id, name: `Node${id}`, region: '', panel_id: id * 10, panel_name: `srv${id}`, enabled: true })
const row = (node: number, id: string, over: Partial<OrderRow> = {}): OrderRow => ({
  kind: 'line', node_id: node, line_id: id, name: `L${node}${id}`, enabled: true, position: 1, positioned: true, line_type: 'forward', published: true, withheld: '',
  address: 'x', landing: landing(node), entry: { panel_id: 7, panel_name: 'GZ', missing: false }, health: { state: 'ok', checked_at: null }, ...over,
})
const none = () => null
function show(rows: OrderRow[], over: Partial<Parameters<typeof TopologyView>[0]> = {}) {
  const onShowInList = vi.fn()
  render(<MemoryRouter><TopologyView rows={rows} colors={new Map()} exitOf={none} admin empty={<p>nothing</p>} onShowInList={onShowInList} onAsList={vi.fn()} {...over} /></MemoryRouter>)
  return onShowInList
}

describe('the topology view', () => {
  it('selecting an entry server offers the list filtered to the lines through it', () => {
    const onShow = show([row(1, 'a'), row(1, 'b'), row(2, 'c', { entry: { panel_id: 8, panel_name: 'SH', missing: false } })])
    fireEvent.click(screen.getByRole('button', { name: /^GZ/ }))
    fireEvent.click(screen.getByRole('button', { name: 'View the 2 lines through "GZ" in the list' }))
    expect(onShow).toHaveBeenCalledWith('entry', '7')
  })

  it('lists a withdrawn line with its state in words in the connections text, not only in colour', () => {
    show([row(1, 'a', { published: false, withheld: 'waiting_entry' })])
    fireEvent.click(screen.getByRole('button', { name: /^GZ/ }))
    expect(screen.getByRole('region', { name: /lines$/ }).textContent).toContain('awaiting to take effect')
  })

  it('does not draw more than 600 edges: it says so and points at the entry and landing filters', () => {
    const many = Array.from({ length: 601 }, (_, i) => row(1, `l${i}`))
    show(many, { filters: <span>the filters</span> })
    expect(screen.getByText('Too many lines to draw clearly in one graph')).toBeTruthy()
    expect(screen.getByText('the filters')).toBeTruthy()
    expect(document.querySelector('[data-slot="relation-graph"]')).toBeNull()
  })

  it('draws a graph at exactly 600 edges', () => {
    show(Array.from({ length: 600 }, (_, i) => row(1, `l${i}`)))
    expect(document.querySelector('[data-slot="relation-graph"]')).not.toBeNull()
  })

  it('shows what the caller gives when the filters leave nothing to draw', () => {
    show([])
    expect(screen.getByText('nothing')).toBeTruthy()
  })
})
