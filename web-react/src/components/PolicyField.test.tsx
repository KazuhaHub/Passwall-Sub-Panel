/** @vitest-environment jsdom */
import { useState } from 'react'
import { ThemeProvider } from '@mui/material'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import common from '@/locales/en-US/common.json'
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string, opts?: Record<string, unknown>) => {
  const copy = (common as unknown as { policy_field?: Record<string, string> }).policy_field
  const text = key.startsWith('common:policy_field.') ? copy?.[key.slice('common:policy_field.'.length)] ?? key : key
  return text.replace(/\{\{(\w+)\}\}/g, (_, name: string) => String(opts?.[name] ?? ''))
} }) }))
import PolicyField from './PolicyField'
afterEach(cleanup)

it('edits a domain-neutral numeric setting and resets it to its served default', () => {
  const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })
  const saved: number[] = []
  function Harness() {
    const [value, setValue] = useState(0)
    return <PolicyField spec={{ key: 'dest_hit_retention_days', label: 'Retention', min: 1, max: 365, tail: true }}
      value={value} defaults={{ dest_hit_retention_days: 7 }} onChange={next => { saved.push(next as number); setValue(next as number) }} />
  }
  render(<ThemeProvider theme={theme}><Harness /></ThemeProvider>)
  const input = screen.getByRole('textbox', { name: 'Retention' })
  expect(screen.getByText('Default')).toBeTruthy()
  expect(screen.getByText('Range 1–365, default 7')).toBeTruthy()
  fireEvent.focus(input)
  fireEvent.change(input, { target: { value: '12' } })
  fireEvent.blur(input)
  expect(saved).toEqual([12])
  fireEvent.click(screen.getByRole('button', { name: 'Reset to default' }))
  expect(saved).toEqual([12, 0])
})
