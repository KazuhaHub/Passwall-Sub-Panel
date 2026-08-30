/** @vitest-environment jsdom */
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { TLS_CIPHER_SUITES, TLSCipherSuitesSelect } from './TLSCipherSuitesSelect'

// vitest.config.ts runs without `globals` or a setup file, so RTL's automatic
// per-test cleanup is never registered — without this every extra case in this
// file would query a DOM still holding the previous render's combobox.
afterEach(cleanup)

describe('TLSCipherSuitesSelect', () => {
  it('opens the supported list on focus and serializes selection with colons', () => {
    const onChange = vi.fn()
    const label = 'TLS 加密套件'
    const suiteA = TLS_CIPHER_SUITES[0]
    const suiteB = TLS_CIPHER_SUITES[1]
    const view = render(
      <TLSCipherSuitesSelect label={label} helperText="helper" value="" onChange={onChange} />,
    )

    const input = screen.getByRole('combobox', { name: label })
    expect(input.getAttribute('placeholder')).toBeNull()
    expect(getComputedStyle(input).position).not.toBe('absolute')
    expect(getComputedStyle(input.closest('.MuiAutocomplete-inputRoot')!).minHeight).toBe('40px')
    fireEvent.focus(input)
    expect(screen.getAllByRole('option')).toHaveLength(TLS_CIPHER_SUITES.length)

    fireEvent.click(screen.getByLabelText(`Add ${suiteA}`))
    expect(onChange).toHaveBeenLastCalledWith(suiteA)

    view.rerender(
      <TLSCipherSuitesSelect label={label} helperText="helper" value={`${suiteA}:${suiteB}`} onChange={onChange} />,
    )
    const selectedInput = screen.getByRole('combobox', { name: label })
    expect(selectedInput.getAttribute('readonly')).not.toBeNull()
    expect(getComputedStyle(selectedInput).position).toBe('absolute')
    expect(screen.getByTitle(`${suiteA}:${suiteB}`).textContent).toBe(`${suiteA}:${suiteB}`)
    fireEvent.focus(selectedInput)
    fireEvent.click(screen.getByLabelText(`Remove ${suiteA}`))
    expect(onChange).toHaveBeenLastCalledWith(suiteB)
  })

  // The field advertises itself as a generated preview, so the only way to
  // change it must be the + / × option buttons. useAutocomplete gates its
  // built-in "Backspace removes the last value" path on `defaultMuiPrevented`
  // rather than defaultPrevented, so preventDefault() alone did not hold it.
  it('ignores Backspace and Delete on the generated preview', () => {
    const onChange = vi.fn()
    const label = 'TLS 加密套件'
    const value = TLS_CIPHER_SUITES.slice(0, 3).join(':')
    render(
      <TLSCipherSuitesSelect label={label} helperText="helper" value={value} onChange={onChange} />,
    )

    const input = screen.getByRole('combobox', { name: label })
    fireEvent.focus(input)
    fireEvent.keyDown(input, { key: 'Backspace' })
    fireEvent.keyDown(input, { key: 'Delete' })
    expect(onChange).not.toHaveBeenCalled()
    expect(screen.getByTitle(value).textContent).toBe(value)
  })
})
