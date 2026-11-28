// @vitest-environment jsdom
import { fireEvent, render, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { installReads, list, mount, server } from './testHarness'
import { serverAddresses, addressHeadline } from './addresses'
import { AddressSummary } from './AddressList'
import ServersPage from './ServersPage'

const reported = ['203.0.113.5', '198.51.100.9', '2001:db8::1', '2001:db8:0:1::7']

describe('serverAddresses', () => {
  it('splits by family, drops blanks and repeats, puts the observed address first, and says whether the node reported any', () => {
    const a = serverAddresses({ address: '198.51.100.9', interface_addresses: [...reported, ' ', '203.0.113.5'] })
    expect(a.v4).toEqual(['198.51.100.9', '203.0.113.5'])
    expect(a.v6).toEqual(['2001:db8::1', '2001:db8:0:1::7'])
    expect(a.reported).toBe(true)
    expect(addressHeadline(a)).toEqual({ shown: ['198.51.100.9', '2001:db8::1'], more: 2 })
    expect(serverAddresses({ address: '198.51.100.9' }).reported).toBe(false)
  })

  it('shows one family alone when the node has only that one', () => {
    expect(addressHeadline(serverAddresses({ interface_addresses: ['2001:db8::1'] }))).toEqual({ shown: ['2001:db8::1'], more: 0 })
  })
})

describe('AddressSummary', () => {
  it('shows the first of each family and the rest as +N, and opens the grouped, copyable list with the observed one marked', async () => {
    render(<AddressSummary s={{ address: '198.51.100.9', interface_addresses: reported }} />)
    const trigger = screen.getByRole('button', { name: 'admin:servers.addresses.open' })
    expect(trigger.textContent).toBe('198.51.100.9·2001:db8::1+2')
    fireEvent.click(trigger)
    const popover = await screen.findByText('admin:servers.addresses.ipv4')
    const body = popover.closest('div')!
    const rows = within(body).getAllByRole('listitem')
    expect(rows.map(r => r.textContent)).toEqual([
      '198.51.100.9admin:servers.addresses.observed', '203.0.113.5', '2001:db8::1', '2001:db8:0:1::7',
    ].map(x => expect.stringContaining(x)))
    expect(within(body).getByText('admin:servers.addresses.ipv6')).toBeTruthy()
    // Every address is a copy button.
    expect(within(body).getAllByRole('button')).toHaveLength(4)
  })

  it('says where the panel sees the node from when that is none of its interface addresses', async () => {
    render(<AddressSummary s={{ address: '192.0.2.77', interface_addresses: ['203.0.113.5'] }} />)
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.addresses.open' }))
    expect(await screen.findByText('admin:servers.addresses.seen_from')).toBeTruthy()
    expect(screen.getByText('192.0.2.77')).toBeTruthy()
  })

  it('renders nothing for a server that reported none', () => {
    const { container } = render(<AddressSummary s={{ address: '198.51.100.9' }} />)
    expect(container.textContent).toBe('')
  })
})

describe('ServersPage addresses', () => {
  it('shows the reported addresses under the name, and the stored address for a server with none', async () => {
    const native = { ...server, id: 1, name: 'tokyo', panel_type: 'psp', url: 'psp://agt_1', address: '198.51.100.9', interface_addresses: reported }
    const old = { ...server, id: 2, name: 'osaka', panel_type: 'psp', url: 'psp://agt_2', address: '192.0.2.4' }
    installReads({ '/admin/servers': list([native, old]) })
    mount(<ServersPage />)
    const tokyo = (await screen.findByText('tokyo')).closest('tr')!
    expect(within(tokyo).getByRole('button', { name: 'admin:servers.addresses.open' }).textContent).toContain('198.51.100.9')
    const osaka = (await screen.findByText('osaka')).closest('tr')!
    expect(within(osaka).queryByRole('button', { name: 'admin:servers.addresses.open' })).toBeNull()
    expect(within(osaka).getByText('psp://agt_2')).toBeTruthy()
  })

  it('lists them all on the server page overview', async () => {
    const native = { ...server, id: 1, name: 'tokyo', panel_type: 'psp', url: 'psp://agt_1', address: '198.51.100.9', interface_addresses: reported }
    installReads({ '/admin/servers': list([native]) })
    mount(<ServersPage />, '/admin/servers/1')
    const title = await screen.findByText('admin:servers.addresses.title')
    const field = title.closest('div')!
    for (const a of reported) expect(within(field).getByText(a)).toBeTruthy()
    expect(within(field).getByText('admin:servers.addresses.observed')).toBeTruthy()
  })
})
