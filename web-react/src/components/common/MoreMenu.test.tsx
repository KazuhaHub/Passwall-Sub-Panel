// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'

vi.mock('react-i18next', async () => (await import('@/test/i18nMock')).reactI18nextMock())

import { MoreMenu } from './MoreMenu'

afterEach(cleanup)

function open() {
  const trigger = screen.getByRole('button', { name: '更多' })
  fireEvent.pointerDown(trigger, { button: 0, pointerType: 'mouse' })
  fireEvent.mouseDown(trigger, { button: 0 })
  fireEvent.click(trigger)
  return trigger
}

describe('MoreMenu', () => {
  it('is an icon button named 更多, with the same words as its tooltip target', () => {
    render(<MemoryRouter><MoreMenu items={[{ id: 'a', label: 'A', onSelect: () => {} }]} /></MemoryRouter>)
    const trigger = screen.getByRole('button', { name: '更多' })
    expect(trigger.getAttribute('aria-label')).toBe('更多')
  })

  it('lists destructive items last, below a divider, and runs the chosen one', async () => {
    const del = vi.fn()
    const edit = vi.fn()
    render(<MemoryRouter><MoreMenu items={[
      { id: 'del', label: '删除节点', destructive: true, onSelect: del },
      { id: 'edit', label: '编辑', onSelect: edit },
    ]} /></MemoryRouter>)
    open()
    const items = await screen.findAllByRole('menuitem')
    // Given in the opposite order, the destructive one still comes last.
    expect(items.map(i => i.textContent)).toEqual(['编辑', '删除节点'])
    expect(screen.getAllByRole('separator')).toHaveLength(1)
    expect(items[1].getAttribute('data-variant')).toBe('destructive')
    fireEvent.click(items[1])
    await waitFor(() => expect(del).toHaveBeenCalledTimes(1))
    expect(edit).not.toHaveBeenCalled()
  })

  it('draws no divider when every item is destructive', async () => {
    render(<MemoryRouter><MoreMenu items={[{ id: 'd', label: '删除', destructive: true, onSelect: () => {} }]} /></MemoryRouter>)
    open()
    await screen.findAllByRole('menuitem')
    expect(screen.queryAllByRole('separator')).toHaveLength(0)
  })

  it('renders a route item as a real link', async () => {
    render(<MemoryRouter><MoreMenu items={[{ id: 'l', label: '打开日志', to: '/admin/logs' }]} /></MemoryRouter>)
    open()
    const item = await screen.findByRole('menuitem', { name: '打开日志' })
    expect(item.getAttribute('href')).toBe('/admin/logs')
  })
})
