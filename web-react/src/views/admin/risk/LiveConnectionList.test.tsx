/** @vitest-environment jsdom */
import { ThemeProvider } from '@mui/material/styles'
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import type { LiveConnection, LiveUser } from '@/api/riskCenter'
import LiveConnectionList from './LiveConnectionList'

vi.mock('@/api/client', () => ({ client: {} }))
// t over the REAL zh-CN admin bundle, flattened as the SPA registers it.
const dict = vi.hoisted(() => ({ current: {} as Record<string, string> }))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (k: string, o?: Record<string, unknown>) => {
      const flat = k.startsWith('admin:') ? k.slice('admin:'.length) : k
      const raw = dict.current[flat] ?? (typeof o?.defaultValue === 'string' ? o.defaultValue : k)
      return raw.replace(/\{\{(\w+)\}\}/g, (m, name: string) => (o && name in o ? String(o[name]) : m))
    },
    i18n: { language: 'zh-CN' },
  }),
}))

import zh from '@/locales/zh-CN/admin.json'
import { flatten, type Nested } from '@/i18n/options'
dict.current = flatten(zh as Nested)

const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })

const conn = (over: Partial<LiveConnection>): LiveConnection => ({
  panel_id: 1, panel_name: 'jp-1', node: 'guid-a', source_key: '198.51.100.1', ip: '198.51.100.1',
  exclusion: '', seen_at: 1_790_000_000, region: null, devices: [], ...over,
})

const alice: LiveUser = {
  user_id: 7, upn: 'alice', display_name: 'Alice', stale_addresses: 0, unread_panels: 0,
  connections: [
    conn({ source_key: '2001:db8:1:2::/64', ip: '2001:db8:1:2::5' }),
    conn({ source_key: '203.0.113.1', ip: '203.0.113.1', exclusion: 'shared' }),
    conn({ source_key: '203.0.113.2', ip: '203.0.113.2', exclusion: 'listed' }),
    conn({ source_key: '203.0.113.3', ip: '203.0.113.3', exclusion: 'infra' }),
    conn({ source_key: '10.0.0.4', ip: '10.0.0.4', exclusion: 'internal' }),
  ],
}

function mount(users: LiveUser[], extra: { onOpenUser?: (id: number) => void } = {}) {
  render(
    <ThemeProvider theme={theme}>
      <LiveConnectionList users={users} deviceWindowHours={24} initiallyOpen {...extra} />
    </ThemeProvider>,
  )
}

afterEach(cleanup)

describe('LiveConnectionList', () => {
  // A source the detector set aside was never judged. In the success colour
  // it would read as "checked, and fine" — the one thing it is not.
  it('excluded sources never render in the success colour', () => {
    mount([alice])

    const chip = (label: string) => screen.getByText(label).closest('.MuiChip-root') as HTMLElement
    for (const label of ['共享出口', '忽略名单', '本机节点 / 中转', '内网']) {
      expect(chip(label).className).not.toContain('MuiChip-colorSuccess')
      expect(chip(label).className).toContain('MuiChip-colorDefault')
    }
    expect(chip('参与判定').className).not.toContain('MuiChip-colorSuccess')
  })

  // The detector counts an IPv6 /64 as one source; the address is shown for
  // recognition, with the source it counts as beneath it when they differ.
  it('shows the source key under an address it does not equal', () => {
    mount([alice])

    const row = screen.getByText('2001:db8:1:2::5').closest('tr') as HTMLElement
    expect(within(row).getByText('来源 2001:db8:1:2::/64')).toBeTruthy()
    const plain = screen.getByText('203.0.113.1').closest('tr') as HTMLElement
    expect(within(plain).queryByText(/^来源 /)).toBeNull()
  })

  // A floor must read as a floor: an account on a panel that could not be
  // read may have connections this list cannot show.
  it('marks an account whose panels were not all read', () => {
    mount([{ ...alice, unread_panels: 1 }])
    expect(screen.getByLabelText('有面板读取失败，这个账号的连接可能不全')).toBeTruthy()
  })

  it('opens the account in the lookup', () => {
    const onOpenUser = vi.fn()
    mount([alice], { onOpenUser })
    fireEvent.click(screen.getByRole('button', { name: '查看用户' }))
    expect(onOpenUser).toHaveBeenCalledWith(7)
  })
})
