// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { NodeSetupPlan } from '@/api/nodes'
import type { Server } from '@/api/servers'
import i18n from '@/test/initI18n'
import '../testHarness'
import { ReviewStep, type ReviewStepProps } from './ReviewStep'
import type { SetupRelay } from './setupModel'

afterEach(cleanup)

// The wording of the keys this step added; the locale files take them from the task that merges the new keys.
i18n.addResourceBundle('en-US', 'admin', { 'nodes.setup.hop_loopback': 'loopback :{{port}}', 'nodes.setup.paths': 'Lines to be created' }, true, true)

const srv = (id: number, name: string) => ({ id, name, panel_type: 'psp', capabilities: [] }) as unknown as Server
const peers = [srv(1, 'Landing server'), srv(2, 'Entry GZ'), srv(3, 'Entry SH'), srv(5, 'Exit US')]

const plan: NodeSetupPlan = {
  token: 't',
  bindings: [
    { panel_id: 1, kind: 'landing', name: 'Tokyo', port: 443, address: 'land.example', sni: ['www.asus.com'], target: 'www.asus.com:443' },
    { panel_id: 2, kind: 'forward', name: 'GZ', port: 20443, address: 'gz.example', front_id: 7 },
    { panel_id: 3, kind: 'tunnel', name: 'Tokyo · SH', port: 31002, loopback: 40001, listen: '10.0.0.3' },
    { panel_id: 1, peer_id: 3, kind: 'link', name: '', port: 31003 },
    { panel_id: 5, peer_id: 1, kind: 'link', name: '', port: 31004 },
    { panel_id: 5, peer_id: 3, kind: 'link', name: '', port: 31005, reused: true },
  ],
}
const entries: SetupRelay[] = [
  { key: 'gz', line: { name: 'GZ', address: 'gz.example', port: 20443, enabled: true, entry_panel_id: 2 } },
  { key: 'sh', line: { name: 'SH', address: 'sh.example', port: 31002, enabled: true, entry_panel_id: 3, mode: 'tunnel' }, tunnel: { listen: '', sni: 'a', target: 'a:443' } },
  { key: 'ext', line: { name: 'Ext', address: 'ext.example', port: 0, enabled: true } },
  { key: 'cdn', line: { name: 'CF', address: '', domain: 'cdn.example.com', port: 0, mode: 'cdn', enabled: true } },
]

function show(over: Partial<ReviewStepProps> = {}) {
  const props: ReviewStepProps = {
    plan, entries, direct: true, peers, landingPort: 443, routeCount: 0, editingLoopbacks: {}, onLoopback: vi.fn(), editingLinkPorts: {}, onLinkPort: vi.fn(),
    changed: false, landingName: 'Tokyo', exitName: 'Exit US', ...over,
  }
  render(<ReviewStep {...props} />)
  return props
}
const diagrams = () => screen.getAllByTestId('review-path').map(p => within(p).getByRole('img').getAttribute('aria-label') ?? '')

describe('the bindings table', () => {
  it('lists every binding with its server, kind, endpoint and SNI, then the entries that have no server', () => {
    show()
    const table = screen.getByRole('table', { name: 'Review ports' })
    const rows = within(table).getAllByRole('row').slice(1)
    const text = rows.map(r => r.textContent ?? '')
    expect(text[0]).toContain('Landing server')
    expect(text[0]).toContain('Direct · Tokyo')
    expect(text[0]).toContain('land.example · All addresses · 443')
    expect(text[0]).toContain('www.asus.com')
    expect(text[0]).toContain('www.asus.com:443')
    expect(text.find(r => r.includes('Direct forward · GZ'))).toContain('Shared by SNI')
    expect(text.find(r => r.includes('Encrypted tunnel · Tokyo · SH'))).toContain('10.0.0.3 · 31002')
    expect(text.find(r => r.includes('Transit tunnel · Entry SH') && r.includes('Existing tunnel'))).toBeTruthy()
    expect(text.at(-2)).toContain('External entry')
    expect(text.at(-2)).toContain('ext.example · 443')
    expect(text.at(-1)).toContain('CDN')
    expect(text.at(-1)).toContain('cdn.example.com · 443')
  })

  it('edits the loopback and the transit port of a new tunnel, but not one that already exists', () => {
    const props = show({ editingLinkPorts: { '1/5': 32000 } })
    const link = screen.getAllByRole('spinbutton', { name: 'Transit port' }) as HTMLInputElement[]
    expect(link.map(i => i.value)).toEqual(['31003', '32000'])
    fireEvent.change(link[0], { target: { value: '31999' } })
    expect(props.onLinkPort).toHaveBeenCalledWith('3/1', 31999)
    const loopback = screen.getByRole('spinbutton', { name: 'Loopback port' }) as HTMLInputElement
    expect(loopback.value).toBe('40001')
    fireEvent.change(loopback, { target: { value: '41000' } })
    expect(props.onLoopback).toHaveBeenCalledWith(3, 41000)
  })
})

describe('the paths', () => {
  it('draws one diagram per planned line, with the ports the plan chose and the exit', () => {
    show()
    const labels = diagrams()
    expect(labels).toHaveLength(5)
    // Direct: the landing's own port, then the tunnel to the exit.
    expect(labels[0]).toContain('Tokyo')
    expect(labels[0]).toContain(':443')
    expect(labels[0]).toContain('Exit US')
    expect(labels[0]).toContain(':31004')
    // The forward: its entry port, and the landing port it hands over to.
    expect(labels[1]).toContain('Entry GZ')
    expect(labels[1]).toContain(':20443')
    // The tunnel: the entry listener, the transit port and the loopback.
    expect(labels[2]).toContain('Entry SH')
    expect(labels[2]).toContain(':31002')
    expect(labels[2]).toContain(':31003')
    expect(labels[2]).toContain('loopback :40001')
    expect(labels[3]).toContain('ext.example')
    expect(labels[4]).toContain('cdn.example.com')
    for (const l of labels.slice(1)) expect(l).toContain('Exit US')
  })

  it('has no direct diagram when the direct line is not published, and no exit hop without an exit', () => {
    show({ direct: false, exitName: null })
    const labels = diagrams()
    expect(labels).toHaveLength(4)
    expect(labels.every(l => !l.includes('Exit US'))).toBe(true)
    expect(screen.getByText('The subscription uses the additional entries.')).toBeTruthy()
  })
})

describe('the notices', () => {
  it('says the ports were edited, the route count, and each plan warning', () => {
    show({ changed: true, routeCount: 2, plan: { ...plan, warnings: ['Port 443 is shared', 'Second warning'] } })
    expect(screen.getByText('Review the changed ports before creating the node.')).toBeTruthy()
    expect(screen.getByText('2 destination routes')).toBeTruthy()
    expect(screen.getByText('Port 443 is shared')).toBeTruthy()
    expect(screen.getByText('Second warning')).toBeTruthy()
    expect(screen.getByText('The subscription includes the direct line.')).toBeTruthy()
  })

  it('shows none of them when there is nothing to say', () => {
    show()
    expect(screen.queryByText('Review the changed ports before creating the node.')).toBeNull()
    expect(screen.queryByText(/destination routes/)).toBeNull()
  })
})
