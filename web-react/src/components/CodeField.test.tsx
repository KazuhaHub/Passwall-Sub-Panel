// @vitest-environment jsdom
import { useState } from 'react'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { CodeField } from './CodeField'

// The stub installed by test/setup.ts stands in for LazyCodeEditor, carrying the same label and description wiring.
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }))
afterEach(() => cleanup())

function Harness({ tab = false, onValue }: { tab?: boolean; onValue: (v: string) => void }) {
  const [value, setValue] = useState('a: 1')
  return <CodeField label="Content" hint="Hint text" language="yaml" tabMovesFocus={tab} value={value} onChange={v => { setValue(v); onValue(v) }} />
}

it('names the editor by its label and hands the typed text to the page unchanged', () => {
  const seen = vi.fn()
  render(<Harness onValue={seen} />)
  const box = screen.getByRole('textbox', { name: 'Content' })
  expect(box.getAttribute('data-language')).toBe('yaml')
  fireEvent.change(box, { target: { value: 'a: 2\nb: 3' } })
  expect(seen).toHaveBeenCalledWith('a: 2\nb: 3')
  expect((box as HTMLTextAreaElement).value).toBe('a: 2\nb: 3')
})

it('tells where Tab goes when it indents, and says nothing when Tab moves on', () => {
  const { unmount } = render(<Harness onValue={() => {}} />)
  expect(screen.getByText(/Hint text/).textContent).toContain('admin:rules.editor_keyboard_hint')
  unmount()
  render(<Harness tab onValue={() => {}} />)
  expect(screen.getByText('Hint text').textContent).not.toContain('editor_keyboard_hint')
})

it('describes the editor by the hint', () => {
  render(<Harness onValue={() => {}} />)
  const box = screen.getByRole('textbox', { name: 'Content' })
  expect(document.getElementById(box.getAttribute('aria-describedby')!)?.textContent).toContain('Hint text')
})
