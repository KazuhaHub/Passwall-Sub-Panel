// @vitest-environment jsdom
import { fireEvent, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { mount } from '@/test/adminSaveHarness'
import type { RelayLine } from '@/api/types'
import type { CloudflareRanges, LineSourceGuard } from '@/api/sourceGuard'
import { parseSourceCIDRs } from '@/api/sourceGuard'
import SourceAddressesField from './SourceAddressesField'

const external: RelayLine = { id: 'L1', name: 'x', address: 'relay.example', port: 443, enabled: true }
const cdn: RelayLine = { id: 'C1', name: 'cf', address: '104.16.0.1', port: 443, enabled: true, mode: 'cdn', domain: 'cdn.example.com' }
const cloudflare: CloudflareRanges = { cidrs: ['173.245.48.0/20', '2400:cb00::/32'], source: 'builtin', builtin_date: '2026-11-18', auto_fetch: true }

function renderField(line: RelayLine, over: Partial<Parameters<typeof SourceAddressesField>[0]> = {}) {
  const props = { line, text: '', onText: vi.fn(), onAdd: vi.fn(), ...over }
  mount(<SourceAddressesField {...props} />)
  return props
}

describe('SourceAddressesField', () => {
  it('reports the typed text', () => {
    const props = renderField(external)
    fireEvent.change(screen.getByRole('textbox'), { target: { value: '203.0.113.0/24' } })
    expect(props.onText).toHaveBeenCalledWith('203.0.113.0/24')
    expect(screen.getByTestId('source-effective').textContent).toBe('admin:nodes.source.effective_unknown')
  })

  it('shows the set in force, read-only', () => {
    const guard: LineSourceGuard = { line_id: 'L1', guarded: true, direct: false, factor_milli: 2000, effective_cidrs: ['93.184.216.34/32', '203.0.113.0/24'], refused: [] }
    renderField(external, { guard })
    expect(screen.getByTestId('source-effective').textContent).toContain('93.184.216.34/32  203.0.113.0/24')
  })

  it('shows the Cloudflare list and where it came from for a CDN line without addresses of its own', () => {
    renderField(cdn, { cloudflare })
    expect(screen.getByTestId('source-effective').textContent).toContain('173.245.48.0/20')
    expect(screen.getByTestId('source-cloudflare').textContent).toContain('admin:nodes.source.cloudflare_builtin')
  })

  it('does not show Cloudflare once the line has addresses of its own', () => {
    renderField(cdn, { cloudflare, text: '203.0.113.0/24' })
    expect(screen.queryByTestId('source-cloudflare')).toBeNull()
  })

  it('says a server that cannot guard needs equal multipliers, and why a line is not published', () => {
    renderField(external, { canGuard: false, guard: { line_id: 'L1', guarded: true, direct: false, factor_milli: 2000, effective_cidrs: [], withheld: 'source_guard_factor', refused: [] } })
    expect(screen.getByTestId('source-cannot-guard')).toBeTruthy()
    expect(screen.getByTestId('source-withheld').textContent).toContain('source_guard_factor')
  })

  it('adds a refused address with one click', () => {
    const guard: LineSourceGuard = { line_id: 'L1', guarded: true, direct: false, factor_milli: 2000, effective_cidrs: [], refused: [{ ip: '198.51.100.7', count: 3, last_seen: '2026-11-18T10:00:00Z', kind: 'unregistered', addable: true }] }
    const props = renderField(external, { guard })
    expect(screen.getByTestId('source-refused').textContent).toContain('admin:nodes.source.refused')
    fireEvent.click(screen.getByRole('button', { name: 'admin:nodes.source.add' }))
    expect(props.onAdd).toHaveBeenCalledWith('198.51.100.7')
  })
})

describe('refused sources', () => {
  const base: LineSourceGuard = { line_id: 'C1', guarded: true, direct: false, factor_milli: 2000, effective_cidrs: [], refused: [] }

  it('offers no Add on a CDN line that uses the Cloudflare default', () => {
    const guard = { ...base, refused: [{ ip: '203.0.113.5', count: 1, last_seen: '2026-11-18T10:00:00Z', kind: 'unregistered' as const, addable: false }] }
    renderField(cdn, { guard })
    expect(screen.getByTestId('source-refused')).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'admin:nodes.source.add' })).toBeNull()
  })

  it('shows a wrong-line hit as such, without Add', () => {
    const guard = { ...base, refused: [{ ip: '192.0.2.9', count: 2, last_seen: '2026-11-18T10:00:00Z', kind: 'wrong_line' as const, addable: false }] }
    renderField(external, { guard })
    expect(screen.getByTestId('source-refused').textContent).toContain('admin:nodes.source.refused_wrong_line')
    expect(screen.queryByRole('button', { name: 'admin:nodes.source.add' })).toBeNull()
  })

  it('says why a listener cannot guard', () => {
    renderField(external, { canGuard: false, cannotGuardReason: 'listener' })
    expect(screen.getByTestId('source-cannot-guard').textContent).toContain('cannot_guard_listener')
  })
})

describe('parseSourceCIDRs', () => {
  it('splits on new lines, spaces and commas', () => {
    expect(parseSourceCIDRs('203.0.113.0/24\n 198.51.100.1, 2001:db8::/32 ;')).toEqual(['203.0.113.0/24', '198.51.100.1', '2001:db8::/32'])
    expect(parseSourceCIDRs('  \n')).toEqual([])
  })
})
