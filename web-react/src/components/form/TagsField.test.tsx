// @vitest-environment jsdom
import '@/test/initI18n'
import { useState } from 'react'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { TagsField } from './TagsField'

afterEach(cleanup)

function Harness({ initial = [] as string[], suggestions = ['Premium', 'HK', 'Streaming'], onValue }: { initial?: string[]; suggestions?: string[]; onValue?: (v: string[]) => void }) {
  const [v, setV] = useState(initial)
  return <TagsField aria-label="Tags" value={v} suggestions={suggestions} onChange={n => { setV(n); onValue?.(n) }} />
}
const box = () => screen.getByRole('combobox', { name: 'Tags' }) as HTMLInputElement

describe('TagsField', () => {
  it('adds a typed tag on Enter and clears the box', () => {
    const onValue = vi.fn()
    render(<Harness onValue={onValue} />)
    fireEvent.change(box(), { target: { value: 'Gaming' } })
    fireEvent.keyDown(box(), { key: 'Enter' })
    expect(onValue).toHaveBeenLastCalledWith(['Gaming'])
    expect(box().value).toBe('')
  })

  it('adds on comma too, trims, and ignores duplicates case-insensitively', () => {
    const onValue = vi.fn()
    render(<Harness initial={['Premium']} onValue={onValue} />)
    fireEvent.change(box(), { target: { value: ' premium ' } })
    fireEvent.keyDown(box(), { key: ',' })
    expect(onValue).not.toHaveBeenCalled()
    fireEvent.change(box(), { target: { value: ' Edge ' } })
    fireEvent.keyDown(box(), { key: ',' })
    expect(onValue).toHaveBeenLastCalledWith(['Premium', 'Edge'])
  })

  it('offers suggestions that are not chosen yet, filtered by what is typed', () => {
    render(<Harness initial={['HK']} />)
    fireEvent.focus(box())
    fireEvent.change(box(), { target: { value: 'st' } })
    const options = screen.getAllByRole('option').map(o => o.textContent)
    expect(options).toContain('Streaming')
    expect(options.some(o => o?.includes('HK'))).toBe(false)
  })

  it('offers the typed text as a clickable new tag, so it can be chosen on a phone', () => {
    const onValue = vi.fn()
    render(<Harness onValue={onValue} />)
    fireEvent.change(box(), { target: { value: 'Brand new' } })
    fireEvent.mouseDown(screen.getByRole('option', { name: /Add “Brand new”/ }))
    fireEvent.click(screen.getByRole('option', { name: /Add “Brand new”/ }))
    expect(onValue).toHaveBeenLastCalledWith(['Brand new'])
  })

  it('picks the highlighted suggestion with the arrow keys and Enter', () => {
    const onValue = vi.fn()
    render(<Harness onValue={onValue} />)
    fireEvent.focus(box())
    fireEvent.keyDown(box(), { key: 'ArrowDown' })
    fireEvent.keyDown(box(), { key: 'ArrowDown' })
    fireEvent.keyDown(box(), { key: 'Enter' })
    expect(onValue).toHaveBeenLastCalledWith(['HK'])
  })

  it('removes a tag with its button and with Backspace on an empty box', () => {
    const onValue = vi.fn()
    render(<Harness initial={['A', 'B']} onValue={onValue} />)
    fireEvent.click(screen.getByRole('button', { name: 'Remove A' }))
    expect(onValue).toHaveBeenLastCalledWith(['B'])
    fireEvent.keyDown(box(), { key: 'Backspace' })
    expect(onValue).toHaveBeenLastCalledWith([])
  })

  it('keeps typed text instead of dropping it when focus leaves', () => {
    const onValue = vi.fn()
    render(<Harness onValue={onValue} />)
    fireEvent.change(box(), { target: { value: 'Pending' } })
    fireEvent.blur(box())
    expect(onValue).toHaveBeenLastCalledWith(['Pending'])
  })

  describe('Chinese input and pasted lists', () => {
    it('does not commit the raw text when Enter confirms an IME candidate', () => {
      const onValue = vi.fn()
      render(<Harness onValue={onValue} />)
      fireEvent.change(box(), { target: { value: 'zhongwen' } })
      fireEvent.keyDown(box(), { key: 'Enter', isComposing: true })
      fireEvent.keyDown(box(), { key: 'Enter', keyCode: 229 })
      expect(onValue).not.toHaveBeenCalled()
      fireEvent.keyDown(box(), { key: ',', isComposing: true })
      expect(onValue).not.toHaveBeenCalled()
      // once the composition is over, Enter commits as usual
      fireEvent.keyDown(box(), { key: 'Enter' })
      expect(onValue).toHaveBeenLastCalledWith(['zhongwen'])
    })

    it('splits a pasted list on commas, full-width commas, semicolons and newlines', () => {
      const onValue = vi.fn()
      render(<Harness initial={['HK']} onValue={onValue} />)
      fireEvent.paste(box(), { clipboardData: { getData: () => ' a ,b，c;d\n e、f , hk ,, a ' } })
      expect(onValue).toHaveBeenLastCalledWith(['HK', 'a', 'b', 'c', 'd', 'e', 'f'])
    })

    it('splits as you type a full-width comma, keeping what follows as the next draft', () => {
      const onValue = vi.fn()
      render(<Harness onValue={onValue} />)
      fireEvent.change(box(), { target: { value: '香港，东' } })
      expect(onValue).toHaveBeenLastCalledWith(['香港'])
      expect(box().value).toBe('东')
    })

    it('splits text that arrives when a composition ends', () => {
      const onValue = vi.fn()
      render(<Harness onValue={onValue} />)
      fireEvent.compositionStart(box())
      fireEvent.change(box(), { target: { value: '甲，乙' } })
      expect(onValue).not.toHaveBeenCalled()
      fireEvent.compositionEnd(box(), { data: '甲，乙' })
      expect(onValue).toHaveBeenLastCalledWith(['甲'])
      expect(box().value).toBe('乙')
    })
  })
})
