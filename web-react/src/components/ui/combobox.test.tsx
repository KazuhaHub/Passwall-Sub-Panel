// @vitest-environment jsdom
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import '@/test/initI18n'
import { Combobox, ComboboxContent, ComboboxInput, ComboboxItem, ComboboxList } from './combobox'

afterEach(cleanup)

it('renders without a Base UI warning and names its clear button', () => {
  const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
  const error = vi.spyOn(console, 'error').mockImplementation(() => {})
  render(
    <Combobox items={['a', 'b']} defaultValue="a">
      <ComboboxInput showClear aria-label="pick" />
      <ComboboxContent><ComboboxList>{(i: string) => <ComboboxItem key={i} value={i}>{i}</ComboboxItem>}</ComboboxList></ComboboxContent>
    </Combobox>,
  )
  expect(screen.getByRole('button', { name: 'Clear' })).toBeTruthy()
  const logged = [...warn.mock.calls, ...error.mock.calls].map(c => String(c[0]))
  expect(logged.filter(m => /nativeButton/.test(m))).toEqual([])
  warn.mockRestore(); error.mockRestore()
})
