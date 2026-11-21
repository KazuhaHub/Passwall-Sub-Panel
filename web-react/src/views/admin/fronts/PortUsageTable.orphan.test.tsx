// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { AxiosError } from 'axios'
import { describe, expect, it, vi } from 'vitest'
import { api, mountAdmin, toast } from '../adminTestHarness'
import PortUsageTable from './PortUsageTable'

vi.mock('@/i18n', () => ({ default: { t: (k: string) => k } }))

const orphan = { port: 40123, bind: 'public', kind: 'listener', node_id: 77, node_name: 'tunnel-77', protocol: 'vless', label: 'orphaned tunnel listener', enabled: true, orphan: true }
const plain = { port: 443, bind: 'public', kind: 'listener', node_id: 3, node_name: 'hk', protocol: 'vless', label: 'hk', enabled: true }

function install(ports: unknown[]) {
  api.get.mockImplementation(async (url: string) => {
    if (url === '/admin/servers/1/ports') return { data: { ports } }
    throw new Error(`Unexpected GET ${url}`)
  })
}
const removeButton = () => screen.findByRole('button', { name: 'Remove' })
async function ask() {
  fireEvent.click(await removeButton())
  return screen.findByRole('alertdialog')
}

describe('PortUsageTable orphaned tunnel listener', () => {
  it('flags an orphan with a remove button and no button on an ordinary listener', async () => {
    install([orphan, plain])
    mountAdmin(<PortUsageTable panelId={1} />)
    await screen.findByText('Orphaned tunnel listener: its line is gone')
    expect(screen.getAllByRole('button', { name: 'Remove' })).toHaveLength(1)
    // The server's own label words it.
    expect(screen.getByText('orphaned tunnel listener')).toBeTruthy()
  })

  it('asks (focus on Cancel), deletes the listener node and reads the table again', async () => {
    install([orphan])
    api.delete.mockResolvedValue({ data: {} })
    mountAdmin(<PortUsageTable panelId={1} />)
    const dialog = await ask()
    await waitFor(() => expect(document.activeElement).toBe(within(dialog).getByRole('button', { name: 'Cancel' })))
    fireEvent.click(within(dialog).getByRole('button', { name: 'Remove' }))
    await waitFor(() => expect(api.delete).toHaveBeenCalledWith('/admin/nodes/77', undefined))
    await waitFor(() => expect(toast).toHaveBeenCalledWith('Orphaned listener removed', 'success'))
    await waitFor(() => expect(api.get.mock.calls.filter(([u]) => u === '/admin/servers/1/ports').length).toBeGreaterThan(1))
  })

  it('deletes nothing when the admin declines', async () => {
    install([orphan])
    mountAdmin(<PortUsageTable panelId={1} />)
    const dialog = await ask()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect(api.delete).not.toHaveBeenCalled()
  })

  it('leaves the table alone when the delete is refused', async () => {
    install([orphan])
    api.delete.mockRejectedValue(new AxiosError('x', '400', undefined, undefined, { status: 400, statusText: '', headers: {}, config: {} as never, data: { error: 'no' } }))
    mountAdmin(<PortUsageTable panelId={1} />)
    const dialog = await ask()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Remove' }))
    await waitFor(() => expect(api.delete).toHaveBeenCalled())
    expect(toast).not.toHaveBeenCalledWith('Orphaned listener removed', 'success')
  })

  it('labels a port promised to a tunnel being set up', async () => {
    install([{ port: 40500, bind: 'public', kind: 'link_planned', label: 'planned', enabled: true }])
    mountAdmin(<PortUsageTable panelId={1} />)
    await screen.findByText('promised to a tunnel being set up')
  })
})
