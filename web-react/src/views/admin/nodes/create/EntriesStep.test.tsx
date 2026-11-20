// @vitest-environment jsdom
import { useState } from 'react'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Server } from '@/api/servers'
import '../testHarness'
import { EntriesStep, type EntriesStepProps } from './EntriesStep'
import { groupServers } from './EntryServerPicker'
import { newSetupRelay, setupProblems, type SetupRelay } from './setupModel'

const fronts = vi.hoisted(() => ({ getServerPorts: vi.fn(), suggestPort: vi.fn() }))
vi.mock('@/api/fronts', () => fronts)

const server = (id: number, name: string, over: Partial<Server> = {}) =>
  ({ id, name, panel_type: 'psp', capabilities: [], address: `${name.toLowerCase().replace(/\W/g, '')}.example`, ...over }) as unknown as Server
const GZ = server(2, 'Entry GZ', { region: 'CN', tags: ['cmcc'] })
const SH = server(3, 'Entry SH', { region: 'CN', tags: ['unicom'] })
const BARE = server(4, 'Bare box')

let keys = 0
afterEach(() => { cleanup(); keys = 0; vi.clearAllMocks() })

/** The step with its state held, as the wizard holds it. */
function mount(over: Partial<EntriesStepProps> & { initial?: SetupRelay[] } = {}) {
  const { initial = [], ...rest } = over
  const seen = { entries: initial, busy: [] as boolean[], direct: true }
  function Host() {
    const [entries, setEntries] = useState<SetupRelay[]>(initial)
    const [direct, setDirect] = useState(true)
    seen.entries = entries
    return (
      <EntriesStep servers={[GZ, SH, BARE]} recent={[]} entries={entries} onChange={setEntries} canTunnel useDirect={direct}
        onDirectChange={d => { seen.direct = d; setDirect(d) }} hostOf={s => s.address ?? ''} newKey={() => `k${++keys}`}
        randomSite={() => 'www.site.test'} tcpTLS={false} onBusyChange={b => seen.busy.push(b)} {...rest} />
    )
  }
  render(<Host />)
  return seen
}

async function pick(name: string) {
  fireEvent.mouseDown(screen.getByRole('combobox'))
  fireEvent.click(await screen.findByRole('option', { name }))
}
const input = (re: RegExp) => screen.getByLabelText(re) as HTMLInputElement

describe('choosing entry servers', () => {
  it('fills the name, the address and the suggested port, and reports busy while it looks', async () => {
    fronts.suggestPort.mockResolvedValue(31001)
    const seen = mount()
    await pick('Entry GZ')
    await waitFor(() => expect(input(/Front port/).value).toBe('31001'))
    expect(input(/Line name/).value).toBe('Entry GZ')
    expect(input(/Front address/).value).toBe('entrygz.example')
    expect(seen.entries[0].line.entry_panel_id).toBe(2)
    expect(seen.busy).toEqual([true, false])
    expect(fronts.getServerPorts).not.toHaveBeenCalled()
  })

  it('takes 443 for TCP+TLS when the server has nothing on it, and the suggestion when it is taken', async () => {
    fronts.suggestPort.mockResolvedValue(31002)
    fronts.getServerPorts.mockResolvedValueOnce([{ port: 80, kind: 'inbound' }])
    mount({ tcpTLS: true })
    await pick('Entry GZ')
    await waitFor(() => expect(input(/Front port/).value).toBe('443'))
    fronts.getServerPorts.mockResolvedValueOnce([{ port: 443, kind: 'inbound' }])
    await pick('Entry SH')
    await waitFor(() => expect(screen.getAllByLabelText(/Front port/).map(i => (i as HTMLInputElement).value)).toEqual(['443', '31002']))
  })

  it('takes 443 over a shared-port front, which can share it', async () => {
    fronts.getServerPorts.mockResolvedValue([{ port: 443, kind: 'front' }])
    mount({ tcpTLS: true })
    await pick('Entry GZ')
    await waitFor(() => expect(input(/Front port/).value).toBe('443'))
  })

  it('leaves the port empty when no suggestion is available, and the entry editable', async () => {
    fronts.suggestPort.mockRejectedValue(new Error('down'))
    mount()
    await pick('Entry GZ')
    await waitFor(() => expect(input(/Line name/).value).toBe('Entry GZ'))
    expect(input(/Front port/).value).toBe('')
    expect(input(/Front address/).disabled).toBe(false)
  })

  it('searches by tag and groups the recently used first, then region and tags, then untagged', async () => {
    mount({ recent: [4] })
    fireEvent.mouseDown(screen.getByRole('combobox'))
    await screen.findAllByRole('option')
    expect(screen.getAllByRole('option').map(o => o.textContent)).toEqual(['Bare box', 'Entry GZ', 'Entry SH'])
    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'unicom' } })
    await waitFor(() => expect(screen.getAllByRole('option').map(o => o.textContent)).toEqual(['Entry SH']))
    expect(groupServers([GZ, SH, BARE], [4], 'Recent', 'Other').map(g => g.value)).toEqual(['Recent', 'CN · cmcc', 'CN · unicom'])
    expect(groupServers([GZ, BARE], [], 'Recent', 'Other').map(g => g.value)).toEqual(['CN · cmcc', 'Other'])
  })

  it('drops the entry of a server that is deselected, and keeps the entries that have no server', async () => {
    fronts.suggestPort.mockResolvedValue(31001)
    const seen = mount({ initial: [{ ...newSetupRelay('x'), line: { name: 'ext', address: 'a.test', port: 0, enabled: true } }] })
    await pick('Entry GZ')
    await waitFor(() => expect(seen.entries).toHaveLength(2))
    fireEvent.keyDown(screen.getByRole('combobox'), { key: 'Escape' })
    fireEvent.click(document.querySelector('[data-slot="combobox-chip-remove"]')!)
    await waitFor(() => expect(seen.entries.map(e => e.key)).toEqual(['x']))
  })
})

describe('an entry', () => {
  const managed = (over: Partial<SetupRelay['line']> = {}): SetupRelay => ({ key: 'm', line: { name: 'GZ', address: 'gz.test', port: 20443, enabled: true, entry_panel_id: 2, ...over } })

  it('switches between direct forward and an encrypted tunnel, filling the tunnel fields once', () => {
    const seen = mount({ initial: [managed()] })
    const mode = screen.getByLabelText(/Forwarding mode/) as HTMLSelectElement
    fireEvent.change(mode, { target: { value: 'tunnel' } })
    expect(seen.entries[0].line.mode).toBe('tunnel')
    expect(seen.entries[0].tunnel).toEqual({ listen: '', sni: 'www.site.test', target: 'www.site.test:443' })
    expect(input(/^SNI/).value).toBe('www.site.test')
    fireEvent.change(input(/REALITY target/), { target: { value: 'www.other.test:443' } })
    fireEvent.change(mode, { target: { value: 'forward' } })
    expect(seen.entries[0].line.mode).toBeUndefined()
    expect(screen.queryByLabelText(/REALITY target/)).toBeNull()
    fireEvent.change(mode, { target: { value: 'tunnel' } })
    expect(seen.entries[0].tunnel?.target).toBe('www.other.test:443')
  })

  it('cannot become a tunnel when the landing is not a Passwall Node server', () => {
    mount({ initial: [managed()], canTunnel: false })
    expect((screen.getByRole('option', { name: 'Encrypted tunnel' }) as HTMLOptionElement).disabled).toBe(true)
  })

  it('offers no mode and no port suggestion for an external entry, whose port may stay empty', () => {
    mount({ initial: [{ key: 'e', line: { name: '', address: '', port: 0, enabled: true } }] })
    expect(screen.queryByLabelText(/Forwarding mode/)).toBeNull()
    expect(screen.queryByRole('button', { name: 'Suggest port' })).toBeNull()
    expect(input(/Front port/).required).toBe(false)
    expect(input(/Front address/).required).toBe(true)
  })

  it('suggests a port on request, and requires one for a managed entry', async () => {
    fronts.suggestPort.mockResolvedValue(40111)
    const seen = mount({ initial: [managed({ port: 0 })] })
    expect(input(/Front port/).required).toBe(true)
    fireEvent.click(screen.getByRole('button', { name: 'Suggest port' }))
    await waitFor(() => expect(seen.entries[0].line.port).toBe(40111))
    expect(fronts.suggestPort).toHaveBeenCalledWith(2)
  })

  it('says what is missing once a field was left, and not before', () => {
    mount({ initial: [managed({ port: 0, address: '' })] })
    expect(screen.queryByText('Enter the entry address')).toBeNull()
    fireEvent.blur(input(/Front address/))
    expect(screen.getByText('Enter the entry address')).toBeTruthy()
    expect(screen.queryByText('Enter the entry port')).toBeNull()
    cleanup()
    mount({ initial: [managed({ port: 0, address: '' })], showErrors: true })
    expect(screen.getByText('Enter the entry port')).toBeTruthy()
  })

  it('removes itself', () => {
    const seen = mount({ initial: [managed()] })
    fireEvent.click(screen.getByRole('button', { name: 'Remove this line' }))
    expect(seen.entries).toEqual([])
  })
})

describe('the direct line and the buttons', () => {
  it('publishes the direct line by a switch', () => {
    const seen = mount()
    const sw = screen.getByRole('switch', { name: 'Include direct line' })
    expect(sw.getAttribute('aria-checked')).toBe('true')
    fireEvent.click(sw)
    expect(seen.direct).toBe(false)
  })

  it('adds an external entry and a CDN line on port 443', () => {
    const seen = mount()
    fireEvent.click(screen.getByRole('button', { name: /External entry/ }))
    fireEvent.click(screen.getByRole('button', { name: /CDN \(e\.g\. Cloudflare\)/ }))
    expect(seen.entries.map(e => [e.line.mode, e.line.port])).toEqual([[undefined, 0], ['cdn', 443]])
    expect(screen.getByTestId('cdn-line')).toBeTruthy()
  })
})

describe('a CDN entry', () => {
  const cdn: SetupRelay = { key: 'c', line: { name: 'CF', mode: 'cdn', domain: 'cdn.example.com', address: '104.16.1.1', port: 443, sni: 'cdn.example.com', host: 'cdn.example.com', enabled: true } }

  it('keeps the saved address, SNI and Host when the domain changes', () => {
    const seen = mount({ initial: [cdn] })
    fireEvent.change(input(/CDN domain/), { target: { value: 'new.example.com' } })
    expect(seen.entries[0].line).toMatchObject({ domain: 'new.example.com', address: '104.16.1.1', sni: 'cdn.example.com', host: 'cdn.example.com' })
  })

  it('defaults client TLS from the port until it is set explicitly', () => {
    const seen = mount({ initial: [cdn] })
    const tls = () => screen.getByRole('switch', { name: 'Clients use TLS to the CDN' }).getAttribute('aria-checked')
    expect(tls()).toBe('true')
    fireEvent.change(input(/^Port/), { target: { value: '8080' } })
    expect(seen.entries[0].line.port).toBe(8080)
    expect(seen.entries[0].line.client_tls).toBeUndefined()
    expect(tls()).toBe('false')
    fireEvent.click(screen.getByRole('switch', { name: 'Clients use TLS to the CDN' }))
    expect(seen.entries[0].line.client_tls).toBe(true)
    fireEvent.change(input(/^Port/), { target: { value: '443' } })
    expect(tls()).toBe('true')
    fireEvent.click(screen.getByRole('switch', { name: 'Clients use TLS to the CDN' }))
    fireEvent.change(input(/^Port/), { target: { value: '8443' } })
    expect(seen.entries[0].line.client_tls).toBe(false)
  })

  it('needs its domain, said once the field was left', () => {
    mount({ initial: [{ ...cdn, line: { ...cdn.line, domain: '' } }] })
    expect(screen.queryByText('A CDN line needs its CDN domain.')).toBeNull()
    fireEvent.blur(input(/CDN domain/))
    expect(screen.getByText('A CDN line needs its CDN domain.')).toBeTruthy()
  })

  it('has no tunnel mode and no server', () => {
    mount({ initial: [cdn] })
    expect(screen.queryByLabelText(/Forwarding mode/)).toBeNull()
    expect(screen.queryByRole('button', { name: 'Suggest port' })).toBeNull()
  })
})

describe('setupProblems', () => {
  it('names what stops each kind of entry', () => {
    expect(setupProblems({ key: 'a', line: { name: '', address: '', port: 0, enabled: true } })).toEqual({ address: 'line.err.address_required' })
    expect(setupProblems({ key: 'a', line: { name: '', address: 'x', port: 0, enabled: true, entry_panel_id: 2 } })).toEqual({ port: 'line.err.port_required' })
    expect(setupProblems({ key: 'a', line: { name: '', address: '', port: 0, mode: 'cdn', enabled: true } })).toEqual({ domain: 'cdn.domain_required' })
    expect(setupProblems({ key: 'a', line: { name: '', address: 'x', port: 1, mode: 'tunnel', enabled: true, entry_panel_id: 2 }, tunnel: { listen: '', sni: '', target: 'a' } }))
      .toEqual({ sni: 'setup.field_required' })
  })
})
