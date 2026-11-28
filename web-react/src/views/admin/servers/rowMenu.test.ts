import type { TFunction } from 'i18next'
import { describe, expect, it, vi } from 'vitest'
import type { Server } from '@/api/servers'
import { rowMenu, type RowMenuHandlers } from './rowMenu'

const t = ((k: string) => k) as unknown as TFunction
const handlers = (): RowMenuHandlers => ({
  open: vi.fn(), editSettings: vi.fn(), metrics: vi.fn(), remove: vi.fn(), upgradeAgent: vi.fn(), upgradePanel: vi.fn(), core: vi.fn(),
})
const server = (extra: Partial<Server> = {}) => ({ id: 1, name: 's', panel_type: 'psp', capabilities: [], ...extra }) as unknown as Server
const menu = (s: Server, h = handlers(), busy = false) => rowMenu(s, h, { t, busy })

describe('a server row menu', () => {
  it('offers open, edit, metrics, the upgrades and delete for a ready Passwall Node server', () => {
    const items = menu(server({ node_upgrade_ready: true, capabilities: ['core.upgrade'] }))
    expect(items.map(i => i.id)).toEqual(['open', 'edit', 'metrics', 'upgrade-agent', 'upgrade-core', 'delete'])
    expect(items.find(i => i.id === 'upgrade-agent')?.disabled).toBe(false)
  })
  it('disables the agent upgrade with a reason when the node is not ready', () => {
    const item = menu(server({ node_upgrade_ready: false })).find(i => i.id === 'upgrade-agent')!
    expect(item.disabled).toBe(true)
    expect(item.disabledReason).toBeTruthy()
  })
  it('disables the upgrades while the server is busy', () => {
    const items = menu(server({ node_upgrade_ready: true, capabilities: ['core.upgrade'] }), handlers(), true)
    expect(items.find(i => i.id === 'upgrade-agent')?.disabled).toBe(true)
    expect(items.find(i => i.id === 'upgrade-core')?.disabled).toBe(true)
  })
  it('offers the panel upgrade for a 3X-UI server, off unless it can, and no metrics', () => {
    const off = menu(server({ panel_type: '3xui' }))
    expect(off.map(i => i.id)).not.toContain('metrics')
    expect(off.find(i => i.id === 'upgrade-panel')?.disabled).toBe(true)
    const on = menu(server({ panel_type: '3xui', capabilities: ['panel.upgrade'] })).find(i => i.id === 'upgrade-panel')!
    expect(on.disabled).toBe(false)
  })
  it('marks delete destructive and puts it last', () => {
    const items = menu(server())
    expect(items.at(-1)).toMatchObject({ id: 'delete', destructive: true })
  })
  it('calls the handler with the server when an item is selected', () => {
    const h = handlers()
    const s = server({ node_upgrade_ready: true })
    menu(s, h).find(i => i.id === 'upgrade-agent')!.onSelect?.()
    expect(h.upgradeAgent).toHaveBeenCalledWith(s)
  })
})
