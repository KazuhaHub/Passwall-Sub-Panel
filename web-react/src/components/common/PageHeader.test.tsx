// @vitest-environment jsdom
import { cleanup, render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'

vi.mock('react-i18next', async () => (await import('@/test/i18nMock')).reactI18nextMock())

import { PageHeader } from './PageHeader'

afterEach(cleanup)
const mount = (ui: React.ReactElement) => render(<MemoryRouter>{ui}</MemoryRouter>)

describe('PageHeader', () => {
  it('is a level-one heading with an optional count and description', () => {
    mount(<PageHeader title="节点" count={12} description="用户订阅里出现的所有节点" />)
    expect(screen.getByRole('heading', { level: 1, name: '节点' })).toBeTruthy()
    expect(document.querySelector('[data-slot="page-header-count"]')?.textContent).toBe('12')
    expect(screen.getByText('用户订阅里出现的所有节点')).toBeTruthy()
  })

  it('shows a zero count (a list that is empty is still a number)', () => {
    mount(<PageHeader title="节点" count={0} />)
    expect(document.querySelector('[data-slot="page-header-count"]')?.textContent).toBe('0')
  })

  it('puts the secondary actions, then the primary, then the more menu, in one group', () => {
    mount(<PageHeader title="用户" primary={<button>新建用户</button>} secondary={<button>导出</button>}
      more={[{ id: 'x', label: '清空', destructive: true, onSelect: () => {} }]} />)
    const group = document.querySelector('[data-slot="page-header-actions"]') as HTMLElement
    const names = within(group).getAllByRole('button').map(b => b.getAttribute('aria-label') ?? b.textContent)
    expect(names).toEqual(['导出', '新建用户', '更多'])
  })

  it('renders no actions area when there are none', () => {
    mount(<PageHeader title="日志" />)
    expect(document.querySelector('[data-slot="page-header-actions"]')).toBeNull()
  })
})
