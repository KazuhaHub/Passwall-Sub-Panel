// @vitest-environment jsdom
import { fireEvent, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { CloudflareRanges, LineSourceGuard } from '@/api/sourceGuard'
import type { RelayLine } from '@/api/types'
import { mountPage } from '../testHarness'
import { SourceAddresses } from './SourceAddresses'

const external: RelayLine = { id: 'L1', name: 'x', address: 'relay.example', port: 443, enabled: true }
const cdn: RelayLine = { id: 'C1', name: 'cf', address: '104.16.0.1', port: 443, enabled: true, mode: 'cdn', domain: 'cdn.example.com' }
const cloudflare: CloudflareRanges = { cidrs: ['173.245.48.0/20', '2400:cb00::/32'], source: 'builtin', builtin_date: '2026-11-18', auto_fetch: true }
const base: LineSourceGuard = { line_id: 'L1', guarded: true, direct: false, factor_milli: 2000, effective_cidrs: [], refused: [] }

function renderField(line: RelayLine, over: Partial<Parameters<typeof SourceAddresses>[0]> = {}) {
  const props = { line, text: '', onText: vi.fn(), onAdd: vi.fn(), ...over }
  mountPage(<SourceAddresses {...props} />)
  return props
}

describe('SourceAddresses', () => {
  it('reports the typed text and shows the effective set read-only', () => {
    const props = renderField(external, { guard: { ...base, effective_cidrs: ['93.184.216.34/32', '203.0.113.0/24'] } })
    fireEvent.change(screen.getByRole('textbox'), { target: { value: '203.0.113.0/24' } })
    expect(props.onText).toHaveBeenCalledWith('203.0.113.0/24')
    expect(screen.getByTestId('source-effective').textContent).toContain('93.184.216.34/32  203.0.113.0/24')
  })

  it('says "not known yet" without a guard', () => {
    renderField(external)
    expect(screen.getByTestId('source-effective').textContent).toBe('Not known yet')
  })

  it('shows the Cloudflare list and where it came from for a CDN line without addresses of its own, and not once it has them', () => {
    renderField(cdn, { cloudflare })
    expect(screen.getByTestId('source-effective').textContent).toContain('173.245.48.0/20')
    expect(screen.getByTestId('source-cloudflare').textContent).toContain('built-in snapshot of 2026-11-18')
  })

  it('hides the Cloudflare note once the line has addresses of its own', () => {
    renderField(cdn, { cloudflare, text: '203.0.113.0/24' })
    expect(screen.queryByTestId('source-cloudflare')).toBeNull()
  })

  it('says a server that cannot guard needs equal multipliers, why a line is not published, and why a listener cannot guard', () => {
    renderField(external, { canGuard: false, cannotGuardReason: 'listener', guard: { ...base, withheld: 'source_guard_factor' } })
    expect(screen.getByTestId('source-cannot-guard').textContent).toContain('lets clients choose the source address')
    expect(screen.getByTestId('source-withheld').textContent).toContain('Not published')
  })

  it('adds a refused address with one click, and offers no Add for a wrong-line hit or a Cloudflare default', () => {
    const refused = [
      { ip: '198.51.100.7', count: 3, last_seen: '2026-11-18T10:00:00Z', kind: 'unregistered' as const, addable: true },
      { ip: '192.0.2.9', count: 2, last_seen: '2026-11-18T10:00:00Z', kind: 'wrong_line' as const, addable: false },
      { ip: '203.0.113.5', count: 1, last_seen: '2026-11-18T10:00:00Z', kind: 'unregistered' as const, addable: false },
    ]
    const props = renderField(external, { guard: { ...base, refused } })
    expect(screen.getAllByTestId('source-refused')).toHaveLength(3)
    const adds = screen.getAllByRole('button', { name: 'Add to source addresses' })
    expect(adds).toHaveLength(1)
    fireEvent.click(adds[0])
    expect(props.onAdd).toHaveBeenCalledWith('198.51.100.7')
  })
})
