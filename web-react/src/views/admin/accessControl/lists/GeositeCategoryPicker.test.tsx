/** @vitest-environment jsdom */
import { ThemeProvider } from '@mui/material'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'

vi.mock('@/api/client', () => ({ client: {} }))
vi.mock('@/query/accessControl', () => ({
  useDestinationCategories: () => ({ data: { updated_at: 1000, categories: [{ name: 'finance', count: 612, regexp_count: 0, attrs: ['cn', '!cn'] }] } }),
  useRefreshDestinationCategories: () => ({ isPending: false, mutateAsync: vi.fn() }),
}))
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key, i18n: { language: 'en-US' } }) }))
import GeositeCategoryPicker from './GeositeCategoryPicker'

afterEach(cleanup)
it('keeps category/attribute indicators and selected-tag removal touch-accessible', () => {
  render(<ThemeProvider theme={createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })}><GeositeCategoryPicker category="finance" attrs="cn,!cn" disabled={false} onChange={vi.fn()} /></ThemeProvider>)
  for (const button of screen.getAllByRole('button')) expect(parseFloat(getComputedStyle(button).minHeight)).toBeGreaterThanOrEqual(44)
  const deletes = document.querySelectorAll('.MuiChip-deleteIcon')
  expect(deletes).toHaveLength(2)
  for (const icon of deletes) expect(parseFloat(getComputedStyle(icon).width)).toBeGreaterThanOrEqual(44)
})
it('retains keyboard removal of literal negative attributes', () => {
  const onChange = vi.fn()
  render(<ThemeProvider theme={createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })}><GeositeCategoryPicker category="finance" attrs="cn,!cn" disabled={false} onChange={onChange} /></ThemeProvider>)
  fireEvent.keyUp(screen.getByRole('button', { name: '!cn' }), { key: 'Delete' })
  expect(onChange).toHaveBeenCalledWith('finance', 'cn')
})
