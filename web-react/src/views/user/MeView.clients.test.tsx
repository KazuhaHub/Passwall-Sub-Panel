// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { installReads, mount } from '@/test/adminSaveHarness'
import MeView from './MeView'

const profile = {
  id: 1,
  upn: 'user@example.test',
  display_name: 'User',
  role: 'user',
  enabled: true,
  sub_url: 'https://sub.example.test/token',
  uuid: 'uuid',
  traffic_reset_period: 'never',
  sub_import_clients: [
    {
      name: 'Clash Verge Rev',
      platforms: ['windows', 'macos', 'linux'],
      import_url_template: 'clash://install-config?url={{ sub_url_encoded }}',
      install_url: 'https://example.test/clash-verge',
      enabled: true,
      sort: 10,
      recommended_for: ['windows', 'macos', 'linux'],
    },
    {
      name: 'Other Client',
      platforms: ['windows'],
      import_url_template: 'other://import?url={{ sub_url_encoded }}',
      install_url: 'https://example.test/other',
      enabled: true,
      sort: 20,
      recommended_for: [],
    },
  ],
}

function installProfileReads() {
  installReads({
    '/user/me': profile,
    '/user/me/traffic': {},
    '/user/me/traffic/history': { points: [] },
  })
}

describe('user client navigation', () => {
  it('shows the recommended card above more clients and links to the client tab', async () => {
    vi.spyOn(window.navigator, 'userAgent', 'get').mockReturnValue('Mozilla/5.0 (Windows NT 10.0; Win64; x64)')
    installProfileReads()
    mount(<MeView />)

    expect(await screen.findByText('Clash Verge Rev')).toBeTruthy()
    const shortcut = screen.getByRole('button', { name: 'user:import.others_title' })
    fireEvent.click(shortcut)

    await waitFor(() => {
      expect(screen.getByRole('tab', { name: 'user:tabs.clients' }).getAttribute('aria-selected')).toBe('true')
    })

    const recommendedLabel = screen.getByText('user:import.recommended_label')
    const moreClientsTitle = screen.getByText('user:import.others_title')
    expect(recommendedLabel.compareDocumentPosition(moreClientsTitle) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'user:import.others_title' })).toBeNull()
    expect(screen.getByText('Other Client')).toBeTruthy()
  })
})
