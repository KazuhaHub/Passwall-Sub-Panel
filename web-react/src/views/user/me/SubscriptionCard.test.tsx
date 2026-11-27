// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import '../testHarness'
import { SubscriptionCard } from './SubscriptionCard'

const card = (subUrl: string, maskSignal = 0) => (
  <SubscriptionCard subUrl={subUrl} maskSignal={maskSignal} onReset={async () => {}} onOpenRules={() => {}} />
)

describe('SubscriptionCard masking', () => {
  it('draws only a blurred decoy code, hidden from assistive tech, and carries no token in the page while hidden', () => {
    const { container } = render(card('https://sub.example.test/TOKEN123'))
    const decoy = screen.getByTestId('qr-placeholder')
    expect(decoy.getAttribute('aria-hidden')).toBe('true')
    expect(decoy.className).toContain('blur')
    expect(container.innerHTML).not.toContain('TOKEN123')
  })

  it('draws a code that differs from the decoy once revealed, so the decoy is not the real link', () => {
    const { container } = render(card('https://sub.example.test/TOKEN123'))
    const hiddenPath = container.querySelector('svg path[fill]:not([fill="#FFFFFF"])')?.getAttribute('d')
    fireEvent.click(screen.getAllByRole('button', { name: 'Show' })[0])
    expect(container.querySelector('svg path[fill]:not([fill="#FFFFFF"])')?.getAttribute('d')).not.toBe(hiddenPath)
  })

  it('draws the QR code once revealed', () => {
    const { container } = render(card('https://sub.example.test/TOKEN123'))
    fireEvent.click(screen.getAllByRole('button', { name: 'Show' })[0])
    expect(screen.queryByTestId('qr-placeholder')).toBeNull()
    expect(container.querySelector('svg path')).toBeTruthy()
  })

  it('is hidden again in the same render when the link changes', () => {
    const { container, rerender } = render(card('https://sub.example.test/TOKEN123'))
    fireEvent.click(screen.getAllByRole('button', { name: 'Show' })[0])
    rerender(card('https://sub.example.test/NEWTOKEN'))
    expect(container.innerHTML).not.toContain('NEWTOKEN')
    expect(screen.getByTestId('qr-placeholder')).toBeTruthy()
  })
})
