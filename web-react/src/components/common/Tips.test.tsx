// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { FieldHint, HelpTip, InfoTip } from './HelpTip'

vi.mock('react-i18next', async () => (await import('@/test/i18nMock')).reactI18nextMock())

afterEach(cleanup)

describe('HelpTip', () => {
  it('names its button, by default and by what a page passes', () => {
    const { rerender } = render(<HelpTip text="说明" />)
    expect(screen.getByRole('button', { name: '查看说明' })).toBeTruthy()
    rerender(<HelpTip text="说明" label="关于此页" />)
    expect(screen.getByRole('button', { name: '关于此页' })).toBeTruthy()
  })

  it('opens the text behind the button, and Escape closes it', async () => {
    render(<HelpTip text="这一页列出所有节点" />)
    expect(screen.queryByText('这一页列出所有节点')).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: '查看说明' }))
    const text = await screen.findByText('这一页列出所有节点')
    fireEvent.keyDown(text, { key: 'Escape' })
    await vi.waitFor(() => expect(screen.queryByText('这一页列出所有节点')).toBeNull())
  })
})

describe('InfoTip', () => {
  it('uses its text as the accessible name and opens it on click, so it works on touch', async () => {
    render(<InfoTip text="倍率会乘到流量上" />)
    const button = screen.getByRole('button', { name: '倍率会乘到流量上' })
    fireEvent.click(button)
    // The name is also on the button, so the popover text is the second match.
    await vi.waitFor(() => expect(screen.getAllByText('倍率会乘到流量上').length).toBeGreaterThan(0))
    expect(document.querySelector('[data-slot="popover-content"]')?.textContent).toBe('倍率会乘到流量上')
  })
})

describe('FieldHint', () => {
  it('shows the summary, keeps the detail behind a click, and never submits a form', async () => {
    const onSubmit = vi.fn((e: { preventDefault: () => void }) => e.preventDefault())
    render(<form onSubmit={onSubmit}><FieldHint summary="将超出额度" detail="更长的说明" /></form>)
    const trigger = screen.getByRole('button', { name: /将超出额度/ })
    expect(trigger.getAttribute('type')).toBe('button')
    expect(screen.queryByText('更长的说明')).toBeNull()
    fireEvent.click(trigger)
    expect(await screen.findByText('更长的说明')).toBeTruthy()
    expect(onSubmit).not.toHaveBeenCalled()
  })

  it('draws a warning in the warning ink and a muted hint in muted text', () => {
    const { rerender } = render(<FieldHint summary="s" detail="d" />)
    expect(screen.getByRole('button').className).toContain('text-warning-ink')
    rerender(<FieldHint summary="s" detail="d" tone="muted" />)
    expect(screen.getByRole('button').className).toContain('text-muted-foreground')
  })
})

describe('touch targets on the explanation triggers', () => {
  it('HelpTip and InfoTip carry the coarse-pointer minimum size', () => {
    render(<><HelpTip text="a" /><InfoTip text="b" /></>)
    for (const b of screen.getAllByRole('button')) expect(b.className).toContain('pointer-coarse:min-h-[2rem]')
  })
})
