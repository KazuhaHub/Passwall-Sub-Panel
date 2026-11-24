// @vitest-environment jsdom
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, expect, it } from 'vitest'
import i18n from '@/test/initI18n'
import { Dialog, DialogContent, DialogTitle } from './dialog'
import { Sheet, SheetContent, SheetTitle } from './sheet'

afterEach(async () => { cleanup(); await i18n.changeLanguage('en-US') })

it('names the built-in close button in the active language', async () => {
  render(<Dialog open><DialogContent><DialogTitle>t</DialogTitle></DialogContent></Dialog>)
  expect(screen.getByRole('button', { name: 'Close' })).toBeTruthy()
  cleanup()
  await i18n.changeLanguage('zh-CN')
  render(<Sheet open><SheetContent><SheetTitle>t</SheetTitle></SheetContent></Sheet>)
  expect(screen.getByRole('button', { name: '关闭' })).toBeTruthy()
})
