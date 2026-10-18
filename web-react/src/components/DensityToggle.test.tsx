// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, expect, it } from 'vitest'
import i18next from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { ThemeProvider } from '@mui/material/styles'
import { createAppTheme } from '@/theme'
import { I18N_STATIC_OPTIONS, flatten } from '@/i18n/options'
import { useAppearanceStore, type Density } from '@/stores/appearance'
import zhCN from '@/locales/zh-CN/appearance.json'
import enUS from '@/locales/en-US/appearance.json'
import DensityToggle from './DensityToggle'

beforeEach(() => {
  localStorage.clear()
  useAppearanceStore.setState({ density: 'comfortable' })
})
afterEach(() => { cleanup(); localStorage.clear(); useAppearanceStore.setState({ density: 'comfortable' }) })

async function mount(language: string) {
  const i18n = i18next.createInstance()
  await i18n.init({ ...I18N_STATIC_OPTIONS, lng: language, resources: {
    'zh-CN': { appearance: flatten(zhCN) },
    'en-US': { appearance: flatten(enUS) },
  } })
  render(<I18nextProvider i18n={i18n}><ThemeProvider theme={createAppTheme({ mode: 'light', sourceColor: '#6750a4', language })}><DensityToggle /></ThemeProvider></I18nextProvider>)
  return i18n
}

it.each([
  ['zh-CN', 'comfortable', '切换为紧凑布局', '切换为舒适布局'],
  ['zh-CN', 'compact', '切换为舒适布局', '切换为紧凑布局'],
  ['en-US', 'comfortable', 'Switch to compact density', 'Switch to comfortable density'],
  ['en-US', 'compact', 'Switch to comfortable density', 'Switch to compact density'],
])('localizes %s tooltip and accessible name starting from %s', async (language, initial, label, nextLabel) => {
  useAppearanceStore.setState({ density: initial as Density })
  await mount(language)
  const button = screen.getByRole('button', { name: label })
  fireEvent.mouseOver(button)
  expect((await screen.findByRole('tooltip')).textContent).toBe(label)
  fireEvent.mouseLeave(button)
  fireEvent.click(button)
  const next = initial === 'compact' ? 'comfortable' : 'compact'
  expect(useAppearanceStore.getState().density).toBe(next)
  expect(localStorage.getItem('psp-user-density')).toBe(next)
  expect(screen.getByRole('button', { name: nextLabel })).toBeTruthy()
  fireEvent.mouseOver(button)
  expect((await screen.findByRole('tooltip')).textContent).toBe(nextLabel)
})

it('updates translations on language change without changing the stored density', async () => {
  const i18n = await mount('zh-CN')
  expect(screen.getByRole('button', { name: '切换为紧凑布局' })).toBeTruthy()
  await act(async () => { await i18n.changeLanguage('en-US') })
  expect(screen.getByRole('button', { name: 'Switch to compact density' })).toBeTruthy()
  expect(useAppearanceStore.getState().density).toBe('comfortable')
  expect(localStorage.getItem('psp-user-density')).toBeNull()
})

it('uses the existing built-in Chinese fallback for an untranslated language', async () => {
  await mount('xx-XX')
  expect(screen.getByRole('button', { name: '切换为紧凑布局' })).toBeTruthy()
})
