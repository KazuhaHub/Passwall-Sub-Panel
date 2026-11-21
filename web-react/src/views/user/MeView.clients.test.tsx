// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { copy, mountMe, serve, snack } from './testHarness'
import MeView from './MeView'

const clients = [
  { name: 'Clash Verge Rev', platforms: ['windows', 'macos', 'linux'], render_format: 'mihomo', import_url_template: 'clash://install-config?url={{ sub_url_encoded }}', install_url: 'https://example.test/clash-verge', enabled: true, sort: 10, recommended_for: ['windows', 'macos', 'linux'] },
  { name: 'Other Client', platforms: ['windows'], render_format: 'mihomo', import_url_template: 'other://import?url={{ sub_url_encoded }}', install_url: 'https://example.test/other', enabled: true, sort: 20, recommended_for: [] },
  { name: 'Plain Client', platforms: ['windows'], render_format: 'mihomo', import_url_template: '{{ sub_url }}', install_url: 'https://example.test/plain', enabled: true, sort: 30, recommended_for: [] },
  { name: 'Off Client', platforms: ['windows'], render_format: 'mihomo', import_url_template: 'off://x', install_url: 'https://example.test/off', enabled: false, sort: 5, recommended_for: [] },
  { name: 'Script Client', platforms: ['windows'], render_format: 'mihomo', import_url_template: 'javascript:alert(1)', install_url: 'https://example.test/s', enabled: true, sort: 40, recommended_for: [] },
]

function onWindows() {
  vi.spyOn(window.navigator, 'userAgent', 'get').mockReturnValue('Mozilla/5.0 (Windows NT 10.0; Win64; x64)')
}

describe('user client navigation', () => {
  it('shows the recommended card above more clients and links to the client tab', async () => {
    onWindows()
    serve({}, { sub_import_clients: clients })
    mountMe(<MeView />)

    await screen.findByText('Clash Verge Rev')
    fireEvent.click(screen.getByRole('button', { name: 'More clients' }))
    await waitFor(() => expect(screen.getByRole('tab', { name: 'Clients' }).getAttribute('aria-selected')).toBe('true'))

    const recommendedLabel = screen.getByText('Recommended client')
    const moreClientsTitle = screen.getByText('More clients')
    expect(recommendedLabel.compareDocumentPosition(moreClientsTitle) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'More clients' })).toBeNull()
    expect(screen.getByText('Other Client')).toBeTruthy()
    // A switched-off client is never listed.
    expect(screen.queryByText('Off Client')).toBeNull()
  })

  it('lists every client under "Import to client" when none is recommended for this device', async () => {
    vi.spyOn(window.navigator, 'userAgent', 'get').mockReturnValue('curl/8')
    serve({}, { sub_import_clients: clients })
    mountMe(<MeView />, '/?tab=clients')
    await screen.findByText('Import to client')
    expect(screen.queryByText('Recommended client')).toBeNull()
    expect(screen.getByText('Clash Verge Rev')).toBeTruthy()
  })
})

describe('importing into a client', () => {
  it('copies a plain https import link instead of navigating to it', async () => {
    onWindows()
    serve({}, { sub_import_clients: clients })
    mountMe(<MeView />, '/?tab=clients')
    const row = (await screen.findByText('Plain Client')).closest('li')!
    fireEvent.click(row.querySelector('button')!)
    await waitFor(() => expect(copy).toHaveBeenCalledWith('https://sub.example.test/secret-token'))
  })

  it('refuses a script-capable scheme in an import template', async () => {
    onWindows()
    serve({}, { sub_import_clients: clients })
    mountMe(<MeView />, '/?tab=clients')
    const row = (await screen.findByText('Script Client')).closest('li')!
    fireEvent.click(row.querySelector('button')!)
    await waitFor(() => expect(snack).toHaveBeenCalledWith('Invalid import link', 'error'))
    expect(copy).not.toHaveBeenCalled()
  })

  it('opens the install page in a new tab without handing it this page', async () => {
    onWindows()
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    serve({}, { sub_import_clients: clients, sub_import_tutorial_url: 'https://example.test/help' })
    mountMe(<MeView />)
    fireEvent.click(await screen.findByRole('button', { name: 'Install' }))
    expect(open).toHaveBeenCalledWith('https://example.test/clash-verge', '_blank', 'noopener,noreferrer')
    fireEvent.click(screen.getByRole('button', { name: 'View tutorial' }))
    expect(open).toHaveBeenCalledWith('https://example.test/help', '_blank', 'noopener,noreferrer')
  })
})
