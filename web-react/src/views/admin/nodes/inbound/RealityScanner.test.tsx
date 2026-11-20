// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { RealityScanResult } from '@/api/nodes'
import { api } from '../testHarness'
import { RealityScanner } from './RealityScanner'

const row = (over: Partial<RealityScanResult> = {}): RealityScanResult => ({
  target: 'www.example.org:443', host: 'www.example.org', ip: '203.0.113.7', port: 443, feasible: true, tls13: true, tlsVersion: 'TLS 1.3', h2: true, alpn: 'h2',
  x25519: true, curveID: 'X25519', certValid: true, certSubject: 'example.org', certIssuer: 'CA', notAfter: '', serverNames: ['www.example.org', 'example.org'], latencyMs: 42, reason: '', ...over,
})
const reply = (items: RealityScanResult[]) => ({ data: { source_panel_id: 4, source_panel_name: 'Tokyo 1', items } })

function open(props: { panelId?: number; onUse?: (r: RealityScanResult) => void; onClose?: () => void } = {}) {
  const onUse = props.onUse ?? vi.fn()
  const onClose = props.onClose ?? vi.fn()
  render(<RealityScanner open panelId={props.panelId ?? 4} sourceName="Tokyo (picked)" onUse={onUse} onClose={onClose} />)
  return { onUse, onClose }
}

describe('RealityScanner', () => {
  it('scans through the selected server when it opens, and names the server that ran the scan', async () => {
    api.post.mockResolvedValue(reply([row()]))
    open()
    // Before the answer the label is the server the person picked.
    expect(screen.getByText('Scan source: Tokyo (picked)')).toBeTruthy()
    await screen.findByText('www.example.org:443')
    expect(api.post).toHaveBeenCalledWith('/admin/nodes/reality-targets/scan', { panel_id: 4, targets: '' }, expect.objectContaining({ timeout: 185_000 }))
    expect(screen.getByText('Scan source: Tokyo 1')).toBeTruthy()
    expect(screen.getByText('Feasible')).toBeTruthy()
  })

  it('does not scan without a server', () => {
    open({ panelId: 0 })
    expect(api.post).not.toHaveBeenCalled()
  })

  it('rescans with what was typed, on the button and on Enter', async () => {
    api.post.mockResolvedValue(reply([row()]))
    open()
    await screen.findByText('www.example.org:443')
    const box = screen.getByPlaceholderText(/comma-separate/)
    fireEvent.change(box, { target: { value: ' 10.0.0.0/24 ' } })
    fireEvent.click(screen.getByRole('button', { name: 'Scan' }))
    await waitFor(() => expect(api.post).toHaveBeenLastCalledWith('/admin/nodes/reality-targets/scan', { panel_id: 4, targets: '10.0.0.0/24' }, expect.anything()))
    fireEvent.keyDown(box, { key: 'Enter' })
    await waitFor(() => expect(api.post).toHaveBeenCalledTimes(3))
  })

  it('hands the chosen row back and closes', async () => {
    const chosen = row()
    api.post.mockResolvedValue(reply([chosen, row({ target: 'other.example:443', feasible: false, reason: 'no TLS 1.3' })]))
    const { onUse, onClose } = open()
    await screen.findByText('www.example.org:443')
    fireEvent.click(screen.getAllByRole('button', { name: 'Use' })[0])
    expect(onUse).toHaveBeenCalledWith(chosen)
    expect(onClose).toHaveBeenCalled()
  })

  it('shows the empty state and no stale rows when the scan fails', async () => {
    api.post.mockResolvedValueOnce(reply([row()])).mockRejectedValueOnce(new Error('offline'))
    open()
    await screen.findByText('www.example.org:443')
    fireEvent.click(screen.getByRole('button', { name: 'Rescan' }))
    await screen.findByText('No scan results')
    expect(screen.queryByText('www.example.org:443')).toBeNull()
  })
})
