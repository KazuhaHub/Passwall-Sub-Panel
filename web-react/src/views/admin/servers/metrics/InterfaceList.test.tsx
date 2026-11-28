// @vitest-environment jsdom
import { fireEvent, render, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { NodeNetworkInterface, NodeNetworkInterfaces } from '@/api/nodeMetrics'
import '../testHarness'
import { InterfaceList, interfaceHeadline, interfaceState } from './InterfaceList'

const link = (over: Partial<NodeNetworkInterface>): NodeNetworkInterface => ({
  name: 'eth0', index: 2, up: true, operational_state: 'up', is_default_ipv4: true, is_default_ipv6: false,
  addresses: [], addresses_truncated: false, ...over,
})

const network = (reported: boolean, interfaces: NodeNetworkInterface[]): NodeNetworkInterfaces => ({ addresses_reported: reported, interfaces })

describe('interfaceState', () => {
  it('separates a disabled link from one without carrier, and reads tun/wg "unknown" as up', () => {
    expect(interfaceState({ up: false, operational_state: 'up' }).key).toBe('disabled')
    expect(interfaceState({ up: true, operational_state: 'lowerlayerdown' }).key).toBe('state_lowerlayerdown')
    expect(interfaceState({ up: true, operational_state: 'unknown' }).key).toBe('up')
    expect(interfaceState({ up: true, operational_state: '' }).key).toBe('up')
  })
})

describe('interfaceHeadline', () => {
  it('shows the first of each family and counts the rest', () => {
    expect(interfaceHeadline(['10.0.0.5/8', '203.0.113.7/24', '2001:db8::7/64'])).toEqual({ shown: ['10.0.0.5/8', '2001:db8::7/64'], more: 1 })
    expect(interfaceHeadline(['2001:db8::7/64'])).toEqual({ shown: ['2001:db8::7/64'], more: 0 })
  })
})

describe('InterfaceList', () => {
  it('lists every interface on one line each and opens the full, copyable address list', async () => {
    render(<InterfaceList network={network(true, [
      link({ addresses: ['10.0.0.5/8', '203.0.113.7/24', '2001:db8::7/64'] }),
      link({ name: 'wg0', index: 5, up: false, is_default_ipv4: false }),
    ])} />)
    const rows = within(screen.getByRole('list', { name: 'admin:servers.interfaces.title' })).getAllByRole('listitem')
    expect(rows).toHaveLength(2)
    expect(rows[0].textContent).toContain('admin:servers.interfaces.default_v4')
    expect(rows[1].textContent).toContain('admin:servers.interfaces.disabled')
    expect(rows[1].textContent).toContain('admin:servers.interfaces.no_address')
    const trigger = screen.getByRole('button', { name: 'admin:servers.interfaces.open' })
    expect(trigger.textContent).toBe('10.0.0.5/8·2001:db8::7/64+1')
    fireEvent.click(trigger)
    const full = await screen.findByRole('button', { name: /203\.0\.113\.7\/24/ })
    expect(full).toBeTruthy()
    expect(screen.queryByText('admin:servers.interfaces.not_reported')).toBeNull()
  })

  it('never reads an older node as having no addresses', () => {
    render(<InterfaceList network={network(false, [link({})])} />)
    expect(screen.queryByText('admin:servers.interfaces.no_address')).toBeNull()
    expect(screen.getByText('—')).toBeTruthy()
    expect(screen.getByText('admin:servers.interfaces.not_reported')).toBeTruthy()
  })

  it('says when the node shortened a list', () => {
    render(<InterfaceList network={network(true, [link({ addresses_truncated: true })])} />)
    expect(screen.getByText('admin:servers.interfaces.truncated')).toBeTruthy()
  })

  it('says there is nothing when the panel or the sample has no interface list', () => {
    render(<InterfaceList network={undefined} />)
    expect(screen.getByText('admin:servers.interfaces.empty')).toBeTruthy()
  })
})
