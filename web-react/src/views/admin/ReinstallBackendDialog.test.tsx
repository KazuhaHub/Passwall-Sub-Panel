// @vitest-environment jsdom
import { fireEvent, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { PanelType, Server } from '@/api/servers'
import { api, mount } from '@/test/adminSaveHarness'
import { useAuthStore } from '@/stores/auth'
import { ReinstallBackendDialog } from './ReinstallBackendDialog'

const labels: Record<PanelType, string> = { psp: 'Passwall Node', '3xui': '3X-UI', sui: 'S-UI' }
function setup(panelType: PanelType) {
  const server: Server = {
    id: 17, name: 'original-server', panel_type: panelType, url: 'https://original.test',
    auth_method: 'token', has_api_token: true, has_password: false, insecure_https: false, capabilities: [],
  }
  const actions = { onClose: vi.fn(), onNativeInstall: vi.fn(), onNativeMigration: vi.fn(), onConfigure: vi.fn() }
  mount(<ReinstallBackendDialog server={server} {...actions} />)
  return { server, ...actions }
}
async function target(panelType: PanelType) {
  fireEvent.mouseDown(screen.getByRole('combobox', { name: 'admin:servers.install_reinstall.backend' }))
  fireEvent.click(await screen.findByRole('option', { name: labels[panelType] }))
}

describe('Reinstall backend selection', () => {
  it.each(['psp', '3xui', 'sui'] as const)('defaults an existing %s record to its original backend, without requests or mutations', panelType => {
    const actions = setup(panelType)
    expect(screen.getByRole('combobox', { name: 'admin:servers.install_reinstall.backend' }).textContent).toBe(labels[panelType])
    expect(api.get).not.toHaveBeenCalled()
    expect(api.post).not.toHaveBeenCalled()
    expect(api.put).not.toHaveBeenCalled()
    expect(actions.onNativeInstall).not.toHaveBeenCalled()
    expect(actions.onNativeMigration).not.toHaveBeenCalled()
    expect(actions.onConfigure).not.toHaveBeenCalled()
  })

  it('lists Passwall Node first but does not select it automatically for a third-party server', async () => {
    setup('sui')
    fireEvent.mouseDown(screen.getByRole('combobox', { name: 'admin:servers.install_reinstall.backend' }))
    expect((await screen.findAllByRole('option')).map(option => option.textContent)).toEqual(['Passwall Node', '3X-UI', 'S-UI'])
  })

  it('continues same-backend Passwall Node installation with the unchanged original record', () => {
    const actions = setup('psp')
    expect(screen.getByText('admin:servers.install_reinstall.native_same')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.install_reinstall.continue' }))
    expect(actions.onNativeInstall).toHaveBeenCalledWith(actions.server)
    expect(actions.onNativeMigration).not.toHaveBeenCalled()
    expect(api.post).not.toHaveBeenCalled()
  })

  it('requires explicit 3X-UI to Passwall Node selection before opening the conversion precheck', async () => {
    const actions = setup('3xui')
    expect(screen.queryByRole('button', { name: 'admin:servers.install_reinstall.precheck' })).toBeNull()
    await target('psp')
    expect(screen.getByText('admin:servers.install_reinstall.switch_warning')).toBeTruthy()
    expect(screen.getByText('admin:servers.install_reinstall.switch_precheck')).toBeTruthy()
    expect(actions.onNativeMigration).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.install_reinstall.precheck' }))
    expect(actions.onNativeMigration).toHaveBeenCalledWith(actions.server)
    expect(actions.onNativeInstall).not.toHaveBeenCalled()
    expect(api.post).not.toHaveBeenCalled()
    expect(api.put).not.toHaveBeenCalled()
  })

  it.each(['3xui', 'sui'] as const)('provides truthful manual %s recovery with disabled unverified automation and configures only the original server', async panelType => {
    const actions = setup(panelType)
    expect(screen.getByText('admin:servers.install_reinstall.manual_unverified')).toBeTruthy()
    expect(screen.getByText('admin:servers.install_reinstall.manual_restore')).toBeTruthy()
    expect(screen.getByText('admin:servers.install_reinstall.manual_configure')).toBeTruthy()
    fireEvent.mouseDown(screen.getByRole('combobox', { name: 'admin:servers.install_reinstall.method' }))
    const automatic = await screen.findByRole('option', { name: 'admin:servers.install_reinstall.automatic_unavailable' })
    expect(automatic.getAttribute('aria-disabled')).toBe('true')
    fireEvent.keyDown(screen.getByRole('listbox'), { key: 'Escape' })
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.install_reinstall.configure_original' }))
    expect(actions.onConfigure).toHaveBeenCalledWith(actions.server)
    expect(actions.onNativeInstall).not.toHaveBeenCalled()
    expect(actions.onNativeMigration).not.toHaveBeenCalled()
    expect(api.post).not.toHaveBeenCalled()
    expect(api.put).not.toHaveBeenCalled()
  })

  it.each([['psp', '3xui'], ['psp', 'sui'], ['3xui', 'sui'], ['sui', 'psp'], ['sui', '3xui']] as const)
    ('disables the unverified %s to %s conversion without reading credentials or writing adapters', async (original, selected) => {
      const actions = setup(original)
      await target(selected)
      expect(screen.getByText('admin:servers.install_reinstall.switch_unavailable')).toBeTruthy()
      const button = screen.getByRole('button', { name: 'admin:servers.install_reinstall.continue' }) as HTMLButtonElement
      expect(button.disabled).toBe(true)
      fireEvent.click(button)
      expect(actions.onNativeInstall).not.toHaveBeenCalled()
      expect(actions.onNativeMigration).not.toHaveBeenCalled()
      expect(actions.onConfigure).not.toHaveBeenCalled()
      expect(api.get).not.toHaveBeenCalled()
      expect(api.post).not.toHaveBeenCalled()
      expect(api.put).not.toHaveBeenCalled()
    })

  it('does not expose backend selection or actionable configuration to non-administrators', () => {
    useAuthStore.setState({ role: 'operator' })
    const actions = setup('psp')
    expect(screen.getByText('admin:servers.install_reinstall.forbidden')).toBeTruthy()
    expect(screen.queryByRole('combobox')).toBeNull()
    expect((screen.getByRole('button', { name: 'admin:servers.install_reinstall.continue' }) as HTMLButtonElement).disabled).toBe(true)
    expect(actions.onNativeInstall).not.toHaveBeenCalled()
    expect(api.get).not.toHaveBeenCalled()
    expect(api.post).not.toHaveBeenCalled()
  })
})
