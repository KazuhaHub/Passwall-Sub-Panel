import { afterEach, describe, expect, it, vi } from 'vitest'

async function loadPanelPath(content?: string) {
  vi.resetModules()
  vi.stubGlobal('document', {
    querySelector: vi.fn(() => content === undefined ? null : { content }),
  })
  return import('./panelPath')
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('panel path helpers', () => {
  it('uses root-relative routes when no panel prefix is configured', async () => {
    const mod = await loadPanelPath()
    expect(mod.panelPath).toBe('')
    expect(mod.panelAPIBase).toBe('/api')
    expect(mod.panelURL()).toBe('/')
    expect(mod.panelURL('/login')).toBe('/login')
    expect(mod.panelURL('login')).toBe('/login')
  })

  it('normalizes trailing slashes and builds prefixed URLs', async () => {
    const mod = await loadPanelPath('  /admin/panel///  ')
    expect(mod.panelPath).toBe('/admin/panel')
    expect(mod.panelAPIBase).toBe('/admin/panel/api')
    expect(mod.panelURL()).toBe('/admin/panel/')
    expect(mod.panelURL('users')).toBe('/admin/panel/users')
    expect(mod.panelURL('///users')).toBe('/admin/panel/users')
  })

  it.each(['panel', 'https://example.com/panel', '   '])(
    'rejects non-path meta content %j',
    async content => {
      const mod = await loadPanelPath(content)
      expect(mod.panelPath).toBe('')
      expect(mod.panelAPIBase).toBe('/api')
    },
  )
})
