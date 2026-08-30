/** @vitest-environment jsdom */
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { TLS_CIPHER_SUITES, TLSCipherSuitesSelect } from './TLSCipherSuitesSelect'

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
})
