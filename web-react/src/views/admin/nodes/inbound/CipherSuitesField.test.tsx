// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import '../testHarness'
import { CipherSuitesField, TLS_CIPHER_SUITES } from './CipherSuitesField'

const LABEL = 'TLS cipher suites'
const [suiteA, suiteB] = TLS_CIPHER_SUITES

describe('CipherSuitesField', () => {
  it('opens the supported list on focus and serializes the selection with colons', () => {
    const onChange = vi.fn()
    const view = render(<CipherSuitesField label={LABEL} hint="helper" value="" onChange={onChange} />)
    const input = screen.getByRole('combobox', { name: LABEL }) as HTMLInputElement
    expect(screen.queryAllByRole('listitem')).toHaveLength(0)
    fireEvent.focus(input)
    expect(screen.getAllByRole('listitem')).toHaveLength(TLS_CIPHER_SUITES.length)

    fireEvent.click(screen.getByLabelText(`Add ${suiteA}`))
    expect(onChange).toHaveBeenLastCalledWith(suiteA)

    view.rerender(<CipherSuitesField label={LABEL} hint="helper" value={`${suiteA}:${suiteB}`} onChange={onChange} />)
    const selected = screen.getByRole('combobox', { name: LABEL }) as HTMLInputElement
    expect(selected.readOnly).toBe(true)
    expect(selected.value).toBe(`${suiteA}:${suiteB}`)
    expect(selected.title).toBe(`${suiteA}:${suiteB}`)
    fireEvent.focus(selected)
    // The picked suite stays in the list; its button now removes it.
    fireEvent.click(screen.getByLabelText(`Remove ${suiteA}`))
    expect(onChange).toHaveBeenLastCalledWith(suiteB)
  })

  // The preview is generated, so the only way to change it is a suite's own button.
  it('ignores Backspace and Delete on the generated preview', () => {
    const onChange = vi.fn()
    const value = TLS_CIPHER_SUITES.slice(0, 3).join(':')
    render(<CipherSuitesField label={LABEL} hint="helper" value={value} onChange={onChange} />)
    const input = screen.getByRole('combobox', { name: LABEL }) as HTMLInputElement
    fireEvent.focus(input)
    fireEvent.keyDown(input, { key: 'Backspace' })
    fireEvent.keyDown(input, { key: 'Delete' })
    expect(onChange).not.toHaveBeenCalled()
    expect(input.value).toBe(value)
  })

  it('closes the list on Escape', () => {
    render(<CipherSuitesField label={LABEL} hint="helper" value="" onChange={vi.fn()} />)
    const input = screen.getByRole('combobox', { name: LABEL })
    fireEvent.focus(input)
    expect(screen.getAllByRole('listitem').length).toBeGreaterThan(0)
    fireEvent.keyDown(input, { key: 'Escape' })
    expect(screen.queryAllByRole('listitem')).toHaveLength(0)
  })
})
