// @vitest-environment jsdom
import { screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { ProbeChip } from '../lines/ProbeChip'
import { mountPage } from '../testHarness'

// The chip of the link quality, carried over from the retired ProbeQualityChip test. The new chip says "withheld" as
// a word of its own label and in its title, not as a separate element, so that is what is asserted.
const q = (over = {}) => ({ state: 'ok' as const, median_rtt_ms: 42.4, loss_percent: 1.2, withheld: false, ...over })
const LABEL = { ok: 'Good', degraded: 'Degraded', down: 'Down' }

describe('ProbeChip as the link quality shows it', () => {
  it.each(['ok', 'degraded', 'down'] as const)('shows %s with rtt and loss', state => {
    mountPage(<ProbeChip probe={q({ state })} />)
    const chip = screen.getByTestId('probe-quality')
    expect(chip.dataset.state).toBe(state)
    expect(chip.textContent).toContain(LABEL[state])
    expect(chip.textContent).toContain('42 ms')
    expect(chip.textContent).toContain('1% loss')
    expect(chip.textContent).not.toContain('Withheld')
  })
  it('shows no data and no numbers when unknown', () => {
    mountPage(<ProbeChip probe={q({ state: 'unknown', median_rtt_ms: 0, loss_percent: 0 })} />)
    const chip = screen.getByTestId('probe-quality')
    expect(chip.dataset.state).toBe('unknown')
    expect(chip.textContent).toBe('No data')
  })
  it('marks withheld from either flag, in the words and in the title', () => {
    const { unmount } = mountPage(<ProbeChip probe={q({ state: 'down' })} withheld />)
    expect(screen.getByTestId('probe-quality').textContent).toContain('Withheld')
    expect(screen.getByTestId('probe-quality').title).toContain('Taken out of subscriptions')
    unmount()
    mountPage(<ProbeChip probe={q({ state: 'down', withheld: true })} />)
    expect(screen.getByTestId('probe-quality').textContent).toContain('Withheld')
  })
  it('renders nothing without a probe', () => {
    mountPage(<ProbeChip />)
    expect(screen.queryByTestId('probe-quality')).toBeNull()
  })
})
