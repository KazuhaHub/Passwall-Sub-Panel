// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

vi.mock('react-i18next', async () => (await import('@/test/i18nMock')).reactI18nextMock())

import { EmptyState } from './EmptyState'
import { ErrorState, describeError } from './ErrorState'
import { CardSkeleton, LoadingState, PageSkeleton, TableSkeleton } from './LoadingState'

afterEach(cleanup)

describe('EmptyState', () => {
  it('says what is missing, and offers the one action when there is one', () => {
    const onClick = vi.fn()
    render(<EmptyState title="还没有节点" description="添加第一个节点后，它会出现在这里" action={<button onClick={onClick}>添加节点</button>} />)
    expect(screen.getByText('还没有节点')).toBeTruthy()
    expect(screen.getByText('添加第一个节点后，它会出现在这里')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '添加节点' }))
    expect(onClick).toHaveBeenCalled()
  })

  it('has no action area when there is nothing to do', () => {
    render(<EmptyState title="没有日志" />)
    expect(screen.queryByRole('button')).toBeNull()
  })
})

describe('ErrorState', () => {
  it('shows the reason and the default next step, and announces itself', () => {
    render(<ErrorState reason={new Error('connect ECONNREFUSED')} />)
    expect(screen.getByRole('alert')).toBeTruthy()
    expect(screen.getByText('无法加载内容')).toBeTruthy()
    expect(screen.getByText('connect ECONNREFUSED')).toBeTruthy()
    expect(screen.getByText(/请检查网络后重试/)).toBeTruthy()
  })

  it('takes the words from an API error body in preference to the transport message', () => {
    expect(describeError({ message: 'Request failed with status code 500', response: { data: { error: '数据库不可用' } } })).toBe('数据库不可用')
    expect(describeError('plain')).toBe('plain')
    expect(describeError(undefined)).toBeUndefined()
  })

  it('retries, and shows its own wait until the retry settles', async () => {
    let finish!: () => void
    const retry = vi.fn(() => new Promise<void>(r => { finish = r }))
    render(<ErrorState retry={retry} />)
    const button = screen.getByRole('button', { name: '重试' }) as HTMLButtonElement
    fireEvent.click(button)
    fireEvent.click(button)
    expect(retry).toHaveBeenCalledTimes(1)
    expect(button.disabled).toBe(true)
    await act(async () => { finish() })
    expect(button.disabled).toBe(false)
  })

  it('offers no retry button without a retry', () => {
    render(<ErrorState />)
    expect(screen.queryByRole('button')).toBeNull()
  })
})

describe('loading skeletons', () => {
  it.each([
    ['LoadingState', <LoadingState key="a" />],
    ['PageSkeleton', <PageSkeleton key="b" />],
    ['TableSkeleton', <TableSkeleton key="c" rows={3} cols={4} />],
    ['CardSkeleton', <CardSkeleton key="d" lines={2} />],
  ])('%s is a busy status region with a label for a screen reader', (_n, el) => {
    render(el)
    const region = screen.getByRole('status')
    expect(region.getAttribute('aria-busy')).toBe('true')
    expect(region.textContent).toBe('加载中')
  })

  it('draws the requested number of table rows', () => {
    const { container } = render(<TableSkeleton rows={4} cols={3} />)
    expect(container.querySelectorAll('[data-slot="skeleton"]')).toHaveLength(12)
  })
})
