// @vitest-environment jsdom
import { ThemeProvider } from '@mui/material/styles'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import HelpTip from './HelpTip'

// t over the REAL bundles' flattened keys is not needed here: the point is
// which KEY the button and the popover ask for, so t echoes it back, with any
// values it was given after a bar.
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (k: string, o?: Record<string, string>) => (o ? `${k}|${Object.values(o).join('|')}` : k),
    i18n: { language: 'en-US' },
  }),
}))

const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })

function mount(node: React.ReactNode) {
  render(<ThemeProvider theme={theme}>{node}</ThemeProvider>)
}

afterEach(cleanup)

describe('HelpTip', () => {
  // The risk center was its first user, and its pages pass no label: they
  // must keep reading the label they always had.
  it('names its button with the risk center label by default', () => {
    mount(<HelpTip textKey="admin:risk_center.help.queue" />)
    expect(screen.getByRole('button', { name: 'admin:risk_center.help.label' })).toBeTruthy()
  })

  it('names its button with the label key a page passes', () => {
    mount(<HelpTip textKey="admin:diagnostics.help" labelKey="admin:diagnostics.help_label" />)
    expect(screen.getByRole('button', { name: 'admin:diagnostics.help_label' })).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'admin:risk_center.help.label' })).toBeNull()
  })

  // One label per button: a card's "?" names the card it explains, so a
  // screen reader does not hear nine identical "About this page" buttons.
  it('fills its label with the values a page passes', () => {
    mount(<HelpTip textKey="admin:diagnostics.cards.poll.purpose" labelKey="admin:diagnostics.help_label_card"
      labelValues={{ title: 'Traffic polling' }} />)
    expect(screen.getByRole('button', { name: 'admin:diagnostics.help_label_card|Traffic polling' })).toBeTruthy()
  })

  it('opens the text behind the button', () => {
    mount(<HelpTip textKey="admin:diagnostics.help" labelKey="admin:diagnostics.help_label" />)
    expect(screen.queryByText('admin:diagnostics.help')).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'admin:diagnostics.help_label' }))
    expect(screen.getByText('admin:diagnostics.help')).toBeTruthy()
  })
})
