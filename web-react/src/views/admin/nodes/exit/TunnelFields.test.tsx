// @vitest-environment jsdom
import { fireEvent, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { TransitLink } from '@/api/nodes'
import { mountPage } from '../testHarness'
import { TunnelFields } from './TunnelFields'

const link = (patch: Partial<TransitLink> = {}): TransitLink => ({
  exists: true, protocol: 'ss2022', exit_address: '', reality_server_name: '', reality_target: '',
  server_address: 'exit.example', up_bytes: 0, down_bytes: 0, ...patch,
})
const REFUSED = /REALITY is unavailable/
const reality = () => screen.getByRole('option', { name: 'VLESS + REALITY' }) as HTMLOptionElement

describe('TunnelFields protocol choice', () => {
  it('disables REALITY and says why when a sing-box entry faces an Xray exit', () => {
    mountPage(<TunnelFields link={link({ reality_refused: true })} onChange={vi.fn()} />)
    expect(screen.getByText(REFUSED)).toBeTruthy()
    expect(reality().disabled).toBe(true)
  })

  it('keeps an already chosen REALITY selectable when it is refused', () => {
    mountPage(<TunnelFields link={link({ reality_refused: true, protocol: 'vless_reality', reality_server_name: 'www.asus.com', reality_target: 'www.asus.com:443' })} onChange={vi.fn()} />)
    expect(reality().disabled).toBe(false)
  })

  it('offers REALITY when the pair allows it', () => {
    mountPage(<TunnelFields link={link()} onChange={vi.fn()} />)
    expect(screen.queryByText(REFUSED)).toBeNull()
    expect(reality().disabled).toBe(false)
  })

  it('fills in a camouflage site and target when switching to REALITY, and only the protocol when switching back', () => {
    const onChange = vi.fn()
    const { unmount } = mountPage(<TunnelFields link={link()} onChange={onChange} />)
    fireEvent.change(screen.getByLabelText('Tunnel protocol'), { target: { value: 'vless_reality' } })
    const patch = onChange.mock.calls[0][0]
    expect(patch.protocol).toBe('vless_reality')
    expect(patch.reality_server_name).toMatch(/^www\./)
    expect(patch.reality_target).toBe(`${patch.reality_server_name}:443`)
    unmount()

    // A site and target already there are kept.
    onChange.mockClear()
    const kept = mountPage(<TunnelFields link={link({ reality_server_name: 'x.example', reality_target: 'x.example:8443' })} onChange={onChange} />)
    fireEvent.change(screen.getByLabelText('Tunnel protocol'), { target: { value: 'vless_reality' } })
    expect(onChange).toHaveBeenCalledWith({ protocol: 'vless_reality', reality_server_name: 'x.example', reality_target: 'x.example:8443' })
    kept.unmount()

    onChange.mockClear()
    mountPage(<TunnelFields link={link({ protocol: 'vless_reality', reality_server_name: 'x.example', reality_target: 'x.example:443' })} onChange={onChange} />)
    fireEvent.change(screen.getByLabelText('Tunnel protocol'), { target: { value: 'ss2022' } })
    expect(onChange).toHaveBeenCalledWith({ protocol: 'ss2022' })
  })
})

describe('TunnelFields addresses', () => {
  it('shows the server address as placeholder and hint, and patches only the address', () => {
    const onChange = vi.fn()
    mountPage(<TunnelFields link={link()} onChange={onChange} />)
    const input = screen.getByLabelText('Exit address') as HTMLInputElement
    expect(input.placeholder).toBe('exit.example')
    expect(screen.getByText(/exit\.example/, { selector: 'p' })).toBeTruthy()
    fireEvent.change(input, { target: { value: '203.0.113.9' } })
    expect(onChange).toHaveBeenCalledWith({ exit_address: '203.0.113.9' })
  })

  it('asks for an address when the exit server has not connected yet', () => {
    mountPage(<TunnelFields link={link({ server_address: '' })} onChange={vi.fn()} />)
    expect(screen.getByText(/has not connected yet/)).toBeTruthy()
  })

  it('shows the REALITY fields only for REALITY, and rolls a different site', () => {
    const onChange = vi.fn()
    const { unmount } = mountPage(<TunnelFields link={link()} onChange={onChange} />)
    expect(screen.queryByLabelText('Camouflage site (SNI)')).toBeNull()
    unmount()
    mountPage(<TunnelFields link={link({ protocol: 'vless_reality', reality_server_name: 'www.asus.com', reality_target: 'www.asus.com:443' })} onChange={onChange} />)
    fireEvent.change(screen.getByLabelText('Camouflage target (host:port)'), { target: { value: 'a.example:443' } })
    expect(onChange).toHaveBeenLastCalledWith({ reality_target: 'a.example:443' })
    fireEvent.click(screen.getByRole('button', { name: 'Random' }))
    const patch = onChange.mock.calls[onChange.mock.calls.length - 1][0]
    expect(patch.reality_server_name).not.toBe('www.asus.com')
    expect(patch.reality_target).toBe(`${patch.reality_server_name}:443`)
  })
})
