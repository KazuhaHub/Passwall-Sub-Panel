/** @vitest-environment jsdom */
import { ThemeProvider } from '@mui/material'
import { act, cleanup, render, screen } from '@testing-library/react'
import { createInstance } from 'i18next'
import { I18nextProvider, initReactI18next } from 'react-i18next'
import { afterEach, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { flatten, I18N_STATIC_OPTIONS } from '@/i18n/options'
import enAdmin from '@/locales/en-US/admin.json'
import zhAdmin from '@/locales/zh-CN/admin.json'
import { destinationBudget, destinationPolicies, destinationStatus } from '@/test/accessControlFixtures'
import { accessVerdict } from '@/utils/accessControl'
import QuotaMeters from './policies/QuotaMeters'
import StatusOverview from './StatusOverview'
import ParseReport from './lists/ParseReport'
import { useAccessTranslation } from './useAccessTranslation'

afterEach(cleanup)
async function languageProvider(language: string) {
  const i18n = createInstance()
  await i18n.use(initReactI18next).init({ ...I18N_STATIC_OPTIONS, lng: language,
    resources: { 'en-US': { admin: { ...flatten(enAdmin), records_one: '{{count}} record', records_other: '{{count}} records', identity: 'Account #{{id}}' } }, 'zh-CN': { admin: flatten(zhAdmin) },
      'de-DE': { admin: { 'access_control.quota.domains': 'Domains' } } } })
  const wrapper = ({ children }: { children: React.ReactNode }) => <I18nextProvider i18n={i18n}><ThemeProvider theme={createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })}>{children}</ThemeProvider></I18nextProvider>
  return { i18n, wrapper }
}
it('formats quota counts for the selected UI language rather than the browser language', async () => {
  const { wrapper } = await languageProvider('de-DE')
  render(<QuotaMeters budget={{ ...destinationBudget, domains: { used: 1234, limit: 50000 } }} />, { wrapper })
  expect(screen.getByRole('progressbar', { name: 'Domains' }).getAttribute('aria-valuetext')).toBe('1.234 / 50.000')
})
it('preserves plural rules and raw account IDs while grouping display counts', async () => {
  const { wrapper } = await languageProvider('en-US')
  function Probe() {
    const { t } = useAccessTranslation('admin')
    return <><p>{t('records', { count: 1 })}</p><p>{t('records', { count: 1234 })}</p><p>{t('identity', { id: 1234 })}</p></>
  }
  render(<Probe />, { wrapper })
  expect(screen.getByText('1 record')).toBeTruthy()
  expect(screen.getByText('1,234 records')).toBeTruthy()
  expect(screen.getByText('Account #1234')).toBeTruthy()
})
it('formats report interpolation while retaining numeric plural selection', async () => {
  const { wrapper } = await languageProvider('en-US')
  render(<ParseReport kind="geosite" report={{ accepted: 12345, rewritten: 0, ignored: 1234, ignored_broad: 1234, samples: [] }} />, { wrapper })
  expect(screen.getByRole('alert').textContent).toContain('1,234')
  expect(screen.getByText(/12,345/)).toBeTruthy()
})
it('updates read and scheduled dates when the interface language changes without altering the live announcement', async () => {
  const { i18n, wrapper } = await languageProvider('zh-CN')
  const readAt = Date.UTC(2026, 9, 7, 19, 30, 0), scheduled = readAt + 90000
  const data = destinationStatus({ next_publish_at: scheduled })
  const props = { data, verdict: accessVerdict(data, destinationPolicies()), failed: false, refreshing: false, readAt, busy: false,
    onRetry: vi.fn().mockResolvedValue(undefined), onOpenNodes: vi.fn(), onOpenLists: vi.fn(), onPublish: vi.fn().mockResolvedValue(undefined), onPause: vi.fn().mockResolvedValue(undefined) }
  const view = render(<StatusOverview {...props} />, { wrapper })
  const zhTime = new Intl.DateTimeFormat('zh-CN', { timeStyle: 'medium' }).format(readAt)
  expect(screen.getByText(`读取于 ${zhTime}`)).toBeTruthy()
  const scheduledDate = new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'medium' }).format(scheduled)
  expect(screen.getByLabelText(`预计下发时间：${scheduledDate}`)).toBeTruthy()
  expect(screen.getByRole('status').textContent).not.toContain(zhTime)
  await act(() => i18n.changeLanguage('en-US'))
  view.rerender(<StatusOverview {...props} />)
  const enTime = new Intl.DateTimeFormat('en-US', { timeStyle: 'medium' }).format(readAt)
  expect(screen.getByText(`Read at ${enTime}`)).toBeTruthy()
  expect(screen.getByRole('status').textContent).not.toContain(enTime)
})
