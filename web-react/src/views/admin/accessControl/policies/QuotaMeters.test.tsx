/** @vitest-environment jsdom */
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { destinationBudget } from '@/test/accessControlFixtures'
import QuotaMeters from './QuotaMeters'
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }))
afterEach(cleanup)
const P = 'admin:access_control.quota.'
it('names each quota and exposes actual counts without including low extra budgets', () => {
  render(<QuotaMeters budget={destinationBudget} />)
  expect(screen.getAllByRole('progressbar')).toHaveLength(4)
  expect(screen.getByRole('progressbar', { name: `${P}domains` }).getAttribute('aria-valuetext')).toBe('0 / 50,000')
  expect(screen.queryByRole('progressbar', { name: `${P}subjects` })).toBeNull()
})
it('adds near-limit subjects and bytes and retains true over-limit counts', () => {
  render(<QuotaMeters budget={{ ...destinationBudget, domains: { used: 51000, limit: 50000 }, subjects: { used: 80, limit: 100 }, bytes: { used: 81, limit: 100 } }} />)
  expect(screen.getAllByRole('progressbar')).toHaveLength(6)
  expect(screen.getByRole('progressbar', { name: `${P}subjects` }).getAttribute('aria-valuenow')).toBe('80')
  const domains = screen.getByRole('progressbar', { name: `${P}domains` })
  expect(domains.getAttribute('aria-valuenow')).toBe('100')
  expect(domains.getAttribute('aria-valuetext')).toBe('51,000 / 50,000')
  expect(screen.getByRole('alert').textContent).toBe(`${P}exceeded`)
})
