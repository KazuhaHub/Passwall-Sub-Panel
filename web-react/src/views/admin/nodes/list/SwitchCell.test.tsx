// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { OrderRow } from '@/api/nodes'
import { SwitchCell } from './cells'
import type { ListRow } from './types'

vi.mock('react-i18next', async importOriginal => ({ ...await importOriginal<typeof import('react-i18next')>(), useTranslation: () => ({ t: (k: string) => k }) }))

afterEach(cleanup)

const item = (line_id: string, extra: Partial<OrderRow> = {}): ListRow => ({
  id: `7:${line_id}`,
  row: { kind: 'line', node_id: 7, line_id, name: 'n', enabled: true, position: 1, positioned: true, landing: { enabled: true, panel_id: 3, name: 'L' } as never, ...extra },
})

describe('SwitchCell', () => {
  it('shows the node switch on a direct row, on, and toggles the node once when clicked', () => {
    const onSelect = vi.fn()
    render(<SwitchCell item={item('direct')} items={[{ id: 'toggle-node', label: 'x', onSelect }]} />)
    const sw = screen.getByRole('switch')
    expect(sw.getAttribute('aria-checked')).toBe('true')
    fireEvent.click(sw)
    expect(onSelect).toHaveBeenCalledTimes(1)
  })

  it('shows the line switch on a relay row, off when the line is disabled', () => {
    render(<SwitchCell item={item('r1', { enabled: false })} items={[{ id: 'toggle-line', label: 'x', onSelect: vi.fn() }]} />)
    expect(screen.getByRole('switch').getAttribute('aria-checked')).toBe('false')
  })

  it('shows nothing when the menu does not offer the matching action', () => {
    const { container } = render(<SwitchCell item={item('r1')} items={[{ id: 'toggle-node', label: 'x', onSelect: vi.fn() }]} />)
    expect(container.textContent).toBe('')
    expect(screen.queryByRole('switch')).toBeNull()
  })
})
