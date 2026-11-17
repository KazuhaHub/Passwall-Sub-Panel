// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { AxiosError } from 'axios'
import { describe, expect, it, vi } from 'vitest'
import { api, mount, snack } from '@/test/adminSaveHarness'
import PortUsageTable from './PortUsageTable'

const confirmation = vi.hoisted(() => vi.fn())
vi.mock('@/components/ConfirmHost', () => ({ confirm: confirmation }))

const orphan = { port: 40123, bind: 'public', kind: 'listener', node_id: 77, node_name: 'tunnel-77', protocol: 'vless', label: 'orphaned tunnel listener', enabled: true, orphan: true }
const plain = { port: 443, bind: 'public', kind: 'listener', node_id: 3, node_name: 'hk', protocol: 'vless', label: 'hk', enabled: true }

function install(ports: unknown[]) {
  api.get.mockImplementation(async (url: string) => {
    if (url === '/admin/servers/1/ports') return { data: { ports } }
    throw new Error(`Unexpected GET ${url}`)
  })
}

describe('PortUsageTable orphaned tunnel listener', () => {
  it('flags an orphan with a remove button and no button on an ordinary listener', async () => {
    install([orphan, plain])
    mount(<PortUsageTable panelId={1} />)
    await screen.findByText('admin:servers.fronts.ports.orphan')
    expect(screen.getAllByRole('button', { name: 'admin:servers.fronts.ports.orphan_remove' })).toHaveLength(1)
    // The server's own label words it.
    expect(screen.getByText('orphaned tunnel listener')).toBeTruthy()
  })

  it('asks, deletes the listener node and reads the table again', async () => {
    install([orphan])
    confirmation.mockResolvedValue(true)
    api.delete.mockResolvedValue({ data: {} })
    mount(<PortUsageTable panelId={1} />)
    fireEvent.click(await screen.findByRole('button', { name: 'admin:servers.fronts.ports.orphan_remove' }))
    await waitFor(() => expect(api.delete).toHaveBeenCalledWith('/admin/nodes/77', undefined))
    await waitFor(() => expect(snack).toHaveBeenCalledWith('admin:servers.fronts.ports.orphan_removed', 'success'))
    await waitFor(() => expect(api.get.mock.calls.filter(([u]) => u === '/admin/servers/1/ports').length).toBeGreaterThan(1))
  })

  it('deletes nothing when the admin declines', async () => {
    install([orphan])
    confirmation.mockResolvedValue(false)
    mount(<PortUsageTable panelId={1} />)
    fireEvent.click(await screen.findByRole('button', { name: 'admin:servers.fronts.ports.orphan_remove' }))
    await waitFor(() => expect(confirmation).toHaveBeenCalled())
    expect(api.delete).not.toHaveBeenCalled()
  })

  it('leaves the table alone when the delete is refused', async () => {
    install([orphan])
    confirmation.mockResolvedValue(true)
    api.delete.mockRejectedValue(new AxiosError('x', '400', undefined, undefined, { status: 400, statusText: '', headers: {}, config: {} as never, data: { error: 'no' } }))
    mount(<PortUsageTable panelId={1} />)
    fireEvent.click(await screen.findByRole('button', { name: 'admin:servers.fronts.ports.orphan_remove' }))
    await waitFor(() => expect(api.delete).toHaveBeenCalled())
    expect(snack).not.toHaveBeenCalledWith('admin:servers.fronts.ports.orphan_removed', 'success')
  })

  it('labels a port promised to a tunnel being set up', async () => {
    install([{ port: 40500, bind: 'public', kind: 'link_planned', label: 'planned', enabled: true }])
    mount(<PortUsageTable panelId={1} />)
    await screen.findByText('admin:servers.fronts.ports.kind.link_planned')
  })
})
