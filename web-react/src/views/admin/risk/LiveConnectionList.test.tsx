/** @vitest-environment jsdom */
import { ThemeProvider } from '@mui/material/styles'
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { useSiteStore } from '@/stores/site'
import { formatMsDualTz } from '@/utils/datetime'
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

afterEach(() => {
  cleanup()
  useSiteStore.setState({ timezone: '' })
})

// A panel timezone no CI browser runs in, so a time rendered in the
// browser's zone cannot pass for one rendered in the panel's.
const PANEL_TZ = 'Pacific/Chatham'

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

  // The upstream's sighting and the inferred device's last fetch read in the
  // panel's timezone like every other time in the admin.
  it('prints the sighting and the device fetch in panel time', async () => {
    useSiteStore.setState({ timezone: PANEL_TZ })
    mount([{ ...alice, connections: [conn({ devices: [{ label: 'Pixel', device_id4: 'ab12', client_type: 'clash-meta',
      ua: 'ClashMeta/1.0', fetches: 3, last_at_ms: 1_789_000_000_000 }] })] }])
    expect(screen.getAllByText(`仍有连接（截至 ${formatMsDualTz(1_790_000_000_000, PANEL_TZ)}）`)).toHaveLength(1)
    fireEvent.mouseOver(screen.getByText('Pixel'))
    const tip = await screen.findByRole('tooltip')
    expect(tip.textContent ?? '').toContain(`3 次拉取，最近 ${formatMsDualTz(1_789_000_000_000, PANEL_TZ)}`)
  })

  // 3X-UI lists an address while any connection from it is open, and its
  // scan stamps every such address with the scan's time: the same time for
  // all of an account's addresses, not when each was last used. The column
  // says what the time is, and why it is not a last-use time.
  it('says a live address is still connected as of the panel’s scan, not when it was last used', async () => {
    mount([alice])
    expect(screen.getByRole('columnheader', { name: /^仍有连接/ })).toBeTruthy()
    const hint = '3X-UI 只报告此刻仍有连接的地址；这里是面板扫描的时间，不是这个地址最后一次使用的时间。'
    fireEvent.mouseOver(screen.getByLabelText(hint))
    expect((await screen.findByRole('tooltip')).textContent).toBe(hint)
    expect(screen.getAllByText(/^仍有连接（截至 .+）$/)).toHaveLength(alice.connections.length)
    expect(screen.queryByText(/面板时钟|最后看到/)).toBeNull()
  })
})
