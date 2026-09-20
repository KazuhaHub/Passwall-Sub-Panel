// @vitest-environment jsdom
import { screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { installReads, mountWithClient } from '@/test/adminSaveHarness'
import { useSiteStore } from '@/stores/site'
import UserLayout from './UserLayout'

vi.mock('@/components/LanguageMenu', () => ({ default: () => null }))

describe('user version placement', () => {
  for (const placement of ['hidden', 'footer', 'header']) {
    it(`uses the effective ${placement} setting instead of the global setting`, async () => {
      useSiteStore.setState({ loaded: true, versionDisplay: 'header', productVersion: 'global-version', footerText: 'Copyright' })
      installReads({ '/user/me': { id: 1, version_display: placement, product_version: '4.0.0' } })
      const { client } = mountWithClient(<UserLayout />)
      await waitFor(() => expect(client.isFetching()).toBe(0))
      if (placement === 'hidden') {
        expect(screen.queryByText('4.0.0')).toBeNull()
      } else {
        const version = await screen.findByText('4.0.0')
        expect(version.closest(placement === 'footer' ? 'footer' : 'header')).not.toBeNull()
        expect(screen.getAllByText('4.0.0')).toHaveLength(1)
      }
      expect(screen.queryByText('global-version')).toBeNull()
    })
  }
})
