// @vitest-environment jsdom
import { describe, expect, it } from 'vitest'
import { fireEvent, render } from '@testing-library/react'

import { QUICK_LINK_ICONS, QuickLinkIcon } from './QuickLinkIcon'

// The names an admin may already have stored, from the Material icon set the picker used before
// the move to lucide. The list is the contract: every one must still draw an icon, and none may be
// renamed or dropped, because existing installs hold these strings in their settings.
const STORED_NAMES = [
  'Description', 'MenuBook', 'HelpOutline', 'InfoOutlined', 'Chat', 'Forum', 'Send', 'Campaign', 'TrendingUp',
  'MonitorHeart', 'Favorite', 'VolunteerActivism', 'Payment', 'Download', 'Link', 'Public', 'Home', 'Settings',
  'Email', 'Star', 'Cloud', 'BugReport',
]

describe('QuickLinkIcon', () => {
  it('offers exactly the stored names, none renamed or dropped', () => {
    expect(QUICK_LINK_ICONS.map(i => i.key)).toEqual(STORED_NAMES)
  })

  it.each(STORED_NAMES)('draws an icon for the stored value mui:%s', name => {
    const { container } = render(<QuickLinkIcon icon={`mui:${name}`} />)
    expect(container.querySelector('svg')).not.toBeNull()
  })

  it('draws nothing for an unknown built-in name, an empty value or blanks', () => {
    for (const v of ['mui:NoSuchIcon', '', '   ']) {
      const { container } = render(<QuickLinkIcon icon={v} />)
      expect(container.innerHTML).toBe('')
    }
  })

  it('draws an http(s) address as an image and hides it again when it fails to load', () => {
    const { container } = render(<QuickLinkIcon icon="https://example.test/i.png" />)
    const img = container.querySelector('img')!
    expect(img.getAttribute('src')).toBe('https://example.test/i.png')
    fireEvent.error(img)
    expect(container.querySelector('img')).toBeNull()
  })

  it('draws anything else as literal text (an emoji)', () => {
    const { container } = render(<QuickLinkIcon icon="🚀" />)
    expect(container.textContent).toBe('🚀')
  })
})
