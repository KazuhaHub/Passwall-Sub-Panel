// @vitest-environment jsdom
import { fireEvent, screen, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { installReads, list, mount, server } from './testHarness'
import ServersPage from './ServersPage'

vi.mock('@/hooks/useMediaQuery', () => ({ useMediaQuery: () => true }))

const withAddresses = { ...server, name: 'tokyo', address: '203.0.113.5', interface_addresses: ['203.0.113.5', '198.51.100.9', '2001:db8::1', '2001:db8::2'] }

describe('the server card on a phone', () => {
  it('keeps the address trigger out of the name button, and opening the list does not open the server', async () => {
    installReads({ '/admin/servers': list([withAddresses]) })
    mount(<ServersPage />)
    const card = (await screen.findByText('tokyo')).closest('li')!
    const trigger = within(card).getByRole('button', { name: 'admin:servers.addresses.open' })
    expect(trigger.closest('button:not([aria-label])')).toBeNull()
    expect(within(card).getAllByRole('button').every(b => b === trigger || !b.contains(trigger))).toBe(true)
    fireEvent.click(trigger)
    expect(await screen.findByText('admin:servers.addresses.ipv4')).toBeTruthy()
    expect(screen.queryByRole('tab', { name: 'admin:servers.detail.tabs.overview' })).toBeNull()
    expect(screen.getByText('tokyo')).toBeTruthy()
  })

  it('opens the server from its name, by keyboard-reachable button', async () => {
    installReads({ '/admin/servers': list([withAddresses]) })
    mount(<ServersPage />)
    fireEvent.click(await screen.findByRole('button', { name: 'tokyo' }))
    expect(await screen.findByRole('tab', { name: 'admin:servers.detail.tabs.overview' })).toBeTruthy()
  })

  it('falls back to the URL when the reported list is only blanks', async () => {
    installReads({ '/admin/servers': list([{ ...server, name: 'blank', interface_addresses: ['', '  '] }]) })
    mount(<ServersPage />)
    const card = (await screen.findByText('blank')).closest('li')!
    expect(within(card).getByText('https://panel.example.test')).toBeTruthy()
    expect(within(card).queryByRole('button', { name: 'admin:servers.addresses.open' })).toBeNull()
  })
})
