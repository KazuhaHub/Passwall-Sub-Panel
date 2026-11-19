// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { Sidebar, SidebarProvider, SidebarMenuAction, SidebarMenuButton, SidebarMenuItem, SidebarMenu, useSidebar } from './sidebar'

// jsdom has no matchMedia; the provider asks it whether the viewport is a phone.
beforeEach(() => {
  window.matchMedia = vi.fn().mockImplementation((query: string) => ({
    matches: false, media: query, addEventListener: vi.fn(), removeEventListener: vi.fn(),
    addListener: vi.fn(), removeListener: vi.fn(), onchange: null, dispatchEvent: vi.fn(),
  }))
  localStorage.clear()
  document.cookie = 'sidebar_state=; max-age=0'
})
afterEach(cleanup)

function State() {
  const { state } = useSidebar()
  return <div data-testid="state">{state}</div>
}

function mount(extra?: React.ReactNode) {
  return render(
    <SidebarProvider>
      <State />
      {extra}
    </SidebarProvider>,
  )
}

describe('SidebarProvider persistence', () => {
  it('remembers the collapsed state in localStorage, with no cookie', () => {
    mount()
    fireEvent.keyDown(window, { key: 'b', ctrlKey: true })
    expect(screen.getByTestId('state').textContent).toBe('collapsed')
    // Same key and encoding as the previous layout, so the choice carries over.
    expect(localStorage.getItem('psp-sidebar-collapsed')).toBe('1')
    expect(document.cookie).not.toContain('sidebar_state')
    fireEvent.keyDown(window, { key: 'b', metaKey: true })
    expect(localStorage.getItem('psp-sidebar-collapsed')).toBe('0')
  })

  it('starts collapsed when the stored choice says so', () => {
    localStorage.setItem('psp-sidebar-collapsed', '1')
    mount()
    expect(screen.getByTestId('state').textContent).toBe('collapsed')
  })
})

describe('the Ctrl/Cmd+B shortcut', () => {
  it.each([
    ['an input', () => <input aria-label="field" />],
    ['a textarea', () => <textarea aria-label="field" />],
    ['a contenteditable region', () => <div aria-label="field" contentEditable suppressContentEditableWarning />],
    ['the code editor', () => <div className="cm-editor"><div aria-label="field" tabIndex={0} /></div>],
  ])('does not fire while typing in %s', (_name, field) => {
    mount(field())
    fireEvent.keyDown(screen.getByLabelText('field'), { key: 'b', ctrlKey: true, bubbles: true })
    expect(screen.getByTestId('state').textContent).toBe('expanded')
  })

  it('still fires from anywhere else', () => {
    mount(<button>plain</button>)
    fireEvent.keyDown(screen.getByText('plain'), { key: 'b', ctrlKey: true })
    expect(screen.getByTestId('state').textContent).toBe('collapsed')
  })
})

describe('density', () => {
  it('sizes the rail and its parts from --control-h, not fixed rem', () => {
    mount(
      <Sidebar collapsible="icon">
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton>x</SidebarMenuButton>
            <SidebarMenuAction>a</SidebarMenuAction>
          </SidebarMenuItem>
        </SidebarMenu>
      </Sidebar>,
    )
    const wrapper = document.querySelector('[data-slot="sidebar-wrapper"]') as HTMLElement
    expect(wrapper.style.getPropertyValue('--sidebar-width-icon')).toContain('--control-h')
    const button = screen.getByText('x').closest('button') as HTMLElement
    expect(button.className).toContain('size-(--control-h)!')
    expect(button.className).not.toContain('size-8')
    const action = screen.getByText('a').closest('button') as HTMLElement
    expect(action.className).toContain('--control-h')
    expect(action.className).not.toMatch(/(^|\s)top-1\.5(\s|$)/)
  })
})

describe('the shortcut and non-text inputs', () => {
  it('still fires from a focused checkbox, which takes no text', () => {
    mount(<input type="checkbox" aria-label="box" />)
    fireEvent.keyDown(screen.getByLabelText('box'), { key: 'b', ctrlKey: true })
    expect(screen.getByTestId('state').textContent).toBe('collapsed')
  })
})
