// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import '../testHarness'
import { SubscriptionCard } from './SubscriptionCard'

const card = (subUrl: string, maskSignal = 0) => (
  <SubscriptionCard subUrl={subUrl} maskSignal={maskSignal} onReset={async () => {}} onOpenRules={() => {}} />
)

describe('SubscriptionCard masking', () => {
  it('draws no QR code and carries no token in the page while hidden', () => {
    const { container } = render(card('https://sub.example.test/TOKEN123'))
    expect(container.querySelector('svg[role="img"], canvas')).toBeNull()
    expect(screen.getByTestId('qr-placeholder')).toBeTruthy()
    expect(container.innerHTML).not.toContain('TOKEN123')
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
