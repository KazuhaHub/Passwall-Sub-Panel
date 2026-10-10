/** @vitest-environment jsdom */
import { ThemeProvider } from '@mui/material'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import type { ReactNode } from 'react'
import { createAppTheme } from '@/theme'
import { destinationBudget } from '@/test/accessControlFixtures'
import TemplateGrid, { type TemplateCatalogProps } from './TemplateGrid'
import TemplateMenu from './TemplateMenu'

vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string, options?: Record<string, unknown>) => key + (options?.name ? ` ${options.name}` : '') }) }))
afterEach(cleanup)
const P = 'admin:access_control.'
function catalogProps(): TemplateCatalogProps {
  return { loading: false, downloading: false, failed: false, disabled: false, onDownload: vi.fn(async () => {}) }
}
function mount(element: ReactNode) { return render(<ThemeProvider theme={createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })}>{element}</ThemeProvider>) }

it('starts exactly one explicit template-menu download through a single semantic action', async () => {
  const props = catalogProps()
  mount(<TemplateMenu {...props} added={new Set()} onOpen={vi.fn()} onCreate={vi.fn()} onBlank={vi.fn()} onFinance={vi.fn()} />)
  expect(props.onDownload).not.toHaveBeenCalled()
  fireEvent.click(screen.getByRole('button', { name: `${P}policies.create` }))
  const download = await screen.findByRole('menuitem', { name: `${P}categories.download` })
  fireEvent.click(download)
  await waitFor(() => expect(props.onDownload).toHaveBeenCalledOnce())
  expect(within(download).queryByRole('button')).toBeNull()
})
it('includes the explicit download in menu keyboard navigation and activates it once with Enter', async () => {
  const props = catalogProps()
  mount(<TemplateMenu {...props} added={new Set()} onOpen={vi.fn()} onCreate={vi.fn()} onBlank={vi.fn()} onFinance={vi.fn()} />)
  fireEvent.click(screen.getByRole('button', { name: `${P}policies.create` }))
  const blank = await screen.findByRole('menuitem', { name: `${P}templates.blank` })
  const download = screen.getByRole('menuitem', { name: `${P}categories.download` })
  fireEvent.keyDown(blank, { key: 'Home' })
  for (let i = 0; i < 4; i++) fireEvent.keyDown(document.activeElement!, { key: 'ArrowDown' })
  expect(document.activeElement).toBe(download)
  fireEvent.keyDown(download, { key: 'Enter' })
  await waitFor(() => expect(props.onDownload).toHaveBeenCalledOnce())
})
it('keeps template-card actions and quota explanations touch-accessible', () => {
  mount(<TemplateGrid {...catalogProps()} budget={{ ...destinationBudget, regexps: { used: 250, limit: 256 } }} added={new Set()} onCreate={vi.fn()} onBlank={vi.fn()} onCreateList={vi.fn()}
    catalog={{ categories: [{ name: 'category-porn', count: 300, regexp_count: 18, source_count: 300, ignored_broad_count: 0, attrs: [] }], updated_at: 1000 }} />)
  const buttons = screen.getAllByRole('button')
  expect(buttons.some(button => button.textContent === `${P}templates.over_summary`)).toBe(true)
  for (const button of buttons) expect(parseFloat(getComputedStyle(button).minHeight), button.textContent || button.getAttribute('aria-label') || undefined).toBeGreaterThanOrEqual(44)
})
it('keeps the template menu trigger and blank/finance choices touch-accessible', async () => {
  mount(<TemplateMenu {...catalogProps()} added={new Set()} onOpen={vi.fn()} onCreate={vi.fn()} onBlank={vi.fn()} onFinance={vi.fn()} />)
  const trigger = screen.getByRole('button', { name: `${P}policies.create` })
  expect(parseFloat(getComputedStyle(trigger).minHeight)).toBeGreaterThanOrEqual(44)
  fireEvent.click(trigger)
  for (const item of await screen.findAllByRole('menuitem')) expect(parseFloat(getComputedStyle(item).minHeight), item.textContent || undefined).toBeGreaterThanOrEqual(44)
})
