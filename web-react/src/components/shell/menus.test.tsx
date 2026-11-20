// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('react-i18next', async () => (await import('@/test/i18nMock')).reactI18nextMock())
const i18nMock = vi.hoisted(() => ({ setLanguage: vi.fn() }))
vi.mock('@/i18n', () => ({
  default: { t: (k: string) => k },
  setLanguage: i18nMock.setLanguage,
  currentLanguage: () => 'zh-CN',
  SUPPORTED_LANGUAGES: ['zh-CN', 'zh-TW', 'en-US', 'fr-FR'],
  isBuiltinLanguage: (l: string) => l !== 'fr-FR',
  serverLanguageMeta: (l: string) => (l === 'fr-FR' ? { code: 'fr-FR', name: 'Français' } : undefined),
}))

import { useAppearanceStore } from '@/stores/appearance'
import { LanguageMenu } from './LanguageMenu'
import { ThemeMenu } from './ThemeMenu'
import { UserMenu } from './UserMenu'
import { UserShell } from './UserShell'

beforeEach(() => {
  localStorage.clear()
  useAppearanceStore.setState({ mode: 'auto', density: 'compact', userColor: null, systemColor: '#0061A4' })
  i18nMock.setLanguage.mockReset()
})
afterEach(cleanup)

function click(el: HTMLElement) {
  // Base UI triggers open on pointer down; a plain click covers the rest.
  fireEvent.pointerDown(el, { button: 0, pointerType: 'mouse' })
  fireEvent.mouseDown(el, { button: 0 })
  fireEvent.click(el)
}

describe('ThemeMenu', () => {
  async function open() {
    render(<ThemeMenu />)
    click(screen.getByRole('button', { name: '外观' }))
    return screen.findByRole('radiogroup', { name: '明暗' })
  }

  it('has one button, named and tooltipped, for mode, colour and density together', async () => {
    await open()
    expect(screen.getByRole('radiogroup', { name: '预设主题色' })).toBeTruthy()
    expect(screen.getByRole('radiogroup', { name: '密度' })).toBeTruthy()
    expect(screen.getByRole('radio', { name: '自动' }).getAttribute('aria-checked')).toBe('true')
    expect(screen.getByRole('radio', { name: '紧凑' }).getAttribute('aria-checked')).toBe('true')
  })

  it('writes the mode and the density to the appearance store', async () => {
    await open()
    fireEvent.click(screen.getByRole('radio', { name: '深色' }))
    expect(useAppearanceStore.getState().mode).toBe('dark')
    expect(screen.getByRole('radio', { name: '深色' }).getAttribute('aria-checked')).toBe('true')
    fireEvent.click(screen.getByRole('radio', { name: '舒适' }))
    expect(useAppearanceStore.getState().density).toBe('comfortable')
    expect(localStorage.getItem('psp-user-density')).toBe('comfortable')
  })

  it('offers the six presets, marks the effective one, and picking one sets the user colour', async () => {
    await open()
    const presets = within(screen.getByRole('radiogroup', { name: '预设主题色' })).getAllByRole('radio')
    expect(presets).toHaveLength(6)
    // The site default (#0061A4, blue) is what is in effect.
    expect(screen.getByRole('radio', { name: '蓝色' }).getAttribute('aria-checked')).toBe('true')
    fireEvent.click(screen.getByRole('radio', { name: '青色' }))
    expect(useAppearanceStore.getState().userColor).toBe('#006A6B')
    expect(screen.getByRole('radio', { name: '青色' }).getAttribute('aria-checked')).toBe('true')
  })

  it('applies a custom colour only when it is a valid hex, and says so when it is not', async () => {
    await open()
    const apply = screen.getByRole('button', { name: '应用' }) as HTMLButtonElement
    const hex = screen.getAllByLabelText('颜色值').find(el => el.tagName === 'INPUT' && (el as HTMLInputElement).type === 'text') as HTMLInputElement
    expect(apply.disabled).toBe(true)
    fireEvent.change(hex, { target: { value: '#12' } })
    expect(apply.disabled).toBe(true)
    expect(screen.getByText(/输入 HEX 色值/)).toBeTruthy()
    fireEvent.change(hex, { target: { value: '#112233' } })
    expect(apply.disabled).toBe(false)
    fireEvent.click(apply)
    expect(useAppearanceStore.getState().userColor).toBe('#112233')
  })

  it('shows where the colour comes from and can return to the system default', async () => {
    useAppearanceStore.setState({ userColor: '#9D2C5F' })
    await open()
    expect(screen.getByText(/我的覆盖/)).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '重置为系统默认色' }))
    expect(useAppearanceStore.getState().userColor).toBeNull()
  })
})

describe('LanguageMenu', () => {
  async function open() {
    render(<LanguageMenu />)
    click(screen.getByRole('button', { name: '语言' }))
    return screen.findAllByRole('menuitemradio')
  }

  it('lists the built-in languages and an uploaded pack under its own name', async () => {
    const items = await open()
    expect(items.map(i => i.textContent)).toEqual(['简体中文', '繁体中文', 'English', 'Français'])
    expect(items[0].getAttribute('aria-checked')).toBe('true')
  })

  it('switches the language through setLanguage', async () => {
    const items = await open()
    fireEvent.click(items[2])
    await waitFor(() => expect(i18nMock.setLanguage).toHaveBeenCalledWith('en-US'))
  })

  it('hands the choice to a controlling caller instead', async () => {
    const onChange = vi.fn()
    render(<LanguageMenu value="en-US" onChange={onChange} />)
    click(screen.getByRole('button', { name: '语言' }))
    const items = await screen.findAllByRole('menuitemradio')
    expect(items[2].getAttribute('aria-checked')).toBe('true')
    fireEvent.click(items[1])
    expect(onChange).toHaveBeenCalledWith('zh-TW')
    expect(i18nMock.setLanguage).not.toHaveBeenCalled()
  })
})

describe('UserMenu', () => {
  it('names the trigger after the account, shows the role, and signs out', async () => {
    const onLogout = vi.fn()
    render(<UserMenu user={{ label: 'kazuha', role: 'operator', onLogout, items: <div data-testid="extra" /> }} />)
    click(screen.getByRole('button', { name: '账号菜单: kazuha' }))
    expect(await screen.findByText('操作员')).toBeTruthy()
    expect(screen.getByTestId('extra')).toBeTruthy()
    fireEvent.click(screen.getByRole('menuitem', { name: '退出登录' }))
    expect(onLogout).toHaveBeenCalled()
  })

  it('shows an initial on the avatar', () => {
    render(<UserMenu user={{ label: 'alice', role: 'user', onLogout: () => {} }} />)
    expect(screen.getByText('A')).toBeTruthy()
  })
})

describe('UserShell', () => {
  it('is a top bar and content with no navigation', () => {
    render(
      <MemoryRouter>
        <UserShell brand={{ name: 'Passwall' }} user={{ label: 'u', role: 'user', onLogout: () => {} }} topBarActions={<button>act</button>} footer={<footer>ft</footer>}>
          <p>body</p>
        </UserShell>
      </MemoryRouter>,
    )
    expect(screen.getByText('body')).toBeTruthy()
    expect(screen.getByText('act')).toBeTruthy()
    expect(screen.getByText('ft')).toBeTruthy()
    expect(screen.getByText('Passwall')).toBeTruthy()
    expect(screen.queryByRole('navigation')).toBeNull()
    expect(document.querySelector('[data-slot="sidebar"]')).toBeNull()
  })

  // The sidebar's shell has a skip link and the user one must too: the top bar's buttons come
  // before the content in tab order on every page.
  it('offers a skip link to the content', () => {
    render(
      <MemoryRouter>
        <UserShell brand={{ name: 'Passwall' }} user={{ label: 'u', role: 'user', onLogout: () => {} }}><p>body</p></UserShell>
      </MemoryRouter>,
    )
    expect(screen.getByText('跳到主要内容').getAttribute('href')).toBe('#main-content')
    expect(document.getElementById('main-content')).toBeTruthy()
  })
})
