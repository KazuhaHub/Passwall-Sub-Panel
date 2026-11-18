// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { TransitLink } from '@/api/nodes'
import TransitLinkFields from './TransitLinkFields'

vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }))

const link = (patch: Partial<TransitLink> = {}): TransitLink => ({
  exists: true, protocol: 'ss2022', exit_address: '', reality_server_name: '', reality_target: '',
  server_address: 'exit.example', up_bytes: 0, down_bytes: 0, ...patch,
})

const openProtocol = () => fireEvent.mouseDown(screen.getByRole('combobox'))
afterEach(cleanup)

describe('TransitLinkFields protocol choice', () => {
  it('disables REALITY and says why when a sing-box entry faces an Xray exit', () => {
    render(<TransitLinkFields link={link({ reality_refused: true })} onChange={vi.fn()} />)
    expect(screen.getByText('admin:nodes.egress.reality_refused')).toBeTruthy()
    openProtocol()
    const option = within(screen.getByRole('listbox')).getByRole('option', { name: 'VLESS + REALITY' })
    expect(option.getAttribute('aria-disabled')).toBe('true')
  })

  it('offers REALITY when the pair allows it', () => {
    render(<TransitLinkFields link={link()} onChange={vi.fn()} />)
    expect(screen.queryByText('admin:nodes.egress.reality_refused')).toBeNull()
    openProtocol()
    const option = within(screen.getByRole('listbox')).getByRole('option', { name: 'VLESS + REALITY' })
    expect(option.getAttribute('aria-disabled')).not.toBe('true')
  })
})
