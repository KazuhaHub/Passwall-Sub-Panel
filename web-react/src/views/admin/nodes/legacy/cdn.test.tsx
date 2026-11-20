// @vitest-environment jsdom
import { fireEvent, screen, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { mount } from '@/test/adminSaveHarness'
import CdnLineFields, { defaultClientTLS } from '../../CdnLineFields'
import type { RelayLine } from '@/api/types'

const cdnLine: RelayLine = {
  id: 'C1', name: 'CF', mode: 'cdn', domain: 'cdn.example.com', address: '104.16.1.1', port: 443, sni: 'cdn.example.com', host: 'cdn.example.com', enabled: true,
  cdn_issues: [
    { code: 'cert_not_covering', warn: true, params: { domain: 'cdn.example.com' } },
    { code: 'port_differs', warn: false, params: { port: '443', origin_port: '8443' } },
  ],
}

// The setup wizard still uses this component (N3 replaces it); the saved-node editor's CDN fields are lines/CdnFields.
describe('CdnLineFields', () => {
  it('shows the fields, the no-probe note and the issues of the saved node', () => {
    mount(<CdnLineFields line={cdnLine} onPatch={vi.fn()} />)
    expect(screen.getByTestId('cdn-line').textContent).toContain('admin:nodes.cdn.no_entry')
    expect(screen.getByLabelText('admin:nodes.cdn.tip')).toBeTruthy()
    expect((screen.getByLabelText(/admin:nodes.cdn.domain/) as HTMLInputElement).value).toBe('cdn.example.com')
    expect((screen.getByLabelText(/admin:nodes.cdn.address/) as HTMLInputElement).value).toBe('104.16.1.1')
    expect((screen.getByLabelText(/admin:nodes.cdn.port/) as HTMLInputElement).value).toBe('443')
    expect((screen.getByLabelText(/admin:nodes.relay.sni/) as HTMLInputElement).value).toBe('cdn.example.com')
    expect((screen.getByLabelText(/admin:nodes.cdn.host/) as HTMLInputElement).value).toBe('cdn.example.com')
    const items = within(screen.getByTestId('cdn-issues')).getAllByRole('listitem')
    expect(items.map(i => i.getAttribute('data-code'))).toEqual(['cert_not_covering', 'port_differs'])
  })

  it('keeps the saved address, SNI and Host when the domain changes', () => {
    const onPatch = vi.fn()
    mount(<CdnLineFields line={{ ...cdnLine, address: cdnLine.domain! }} onPatch={onPatch} />)
    fireEvent.change(screen.getByLabelText(/admin:nodes.cdn.domain/), { target: { value: 'new.example.com' } })
    expect(onPatch).toHaveBeenCalledWith({ domain: 'new.example.com' })
  })
})

describe('CdnLineFields client TLS', () => {
  it('defaults from the port and follows it until the admin sets it', () => {
    expect(defaultClientTLS(443)).toBe(true)
    expect(defaultClientTLS(8443)).toBe(true)
    expect([80, 8080, 8880, 2052, 2082, 2086, 2095].some(defaultClientTLS)).toBe(false)
    const onPatch = vi.fn()
    mount(<CdnLineFields line={cdnLine} onPatch={onPatch} />)
    expect((screen.getByLabelText(/admin:nodes.cdn.client_tls$/) as HTMLInputElement).checked).toBe(true)
    fireEvent.change(screen.getByLabelText(/admin:nodes.cdn.port/), { target: { value: '8080' } })
    expect(onPatch).toHaveBeenLastCalledWith({ port: 8080 })
  })

  it('keeps an explicit TLS choice even when it matches the old port default', () => {
    const onPatch = vi.fn()
    mount(<CdnLineFields line={{ ...cdnLine, client_tls: true }} onPatch={onPatch} />)
    fireEvent.change(screen.getByLabelText(/admin:nodes.cdn.port/), { target: { value: '8080' } })
    expect(onPatch).toHaveBeenLastCalledWith({ port: 8080 })
    fireEvent.click(screen.getByLabelText(/admin:nodes.cdn.client_tls$/))
    expect(onPatch).toHaveBeenLastCalledWith({ client_tls: false })
  })

  it('says why a landing cannot go through a CDN from the code, in the locale', () => {
    const line = { ...cdnLine, cdn_issues: [{ code: 'landing_not_capable', warn: true, params: { reason: 'English reason', reason_code: 'reality', node: 'JP' } }] }
    mount(<CdnLineFields line={line} onPatch={vi.fn()} />)
    expect(screen.getByTestId('cdn-issues').textContent).toBe('admin:nodes.cdn.reason.reality')
  })
})
