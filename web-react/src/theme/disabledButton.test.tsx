/** @vitest-environment jsdom */
import { describe, expect, it } from 'vitest'
import { render } from '@testing-library/react'
import { alpha, Button, ThemeProvider } from '@mui/material'
import { createAppTheme } from './index'

// A disabled button that still paints itself in the brand colour reads as
// clickable, and clicking it does nothing at all. On the emergency-access card
// the status line beside it says "your account has not expired and has not
// exceeded its traffic limit yet" — so a live-looking button contradicts the
// sentence explaining why it is unavailable, and the card reads as broken.
//
// The cause was CSS specificity in the THEME, not in any one view:
//
//   theme override   .MuiButton-root.MuiButton-contained.MuiButton-colorPrimary   (0,3,0)
//   MUI's disabled   .MuiButton-root.Mui-disabled                                 (0,2,0)
//
// so the override repainted every disabled contained-primary button in the app.
// It also outranks a view's plain `sx` bgcolor (0,1,0), so no view could fix
// this locally even by trying.

const theme = createAppTheme({ mode: 'light', sourceColor: '#00639A', language: 'en-US' })
const md = theme.palette.md

// getComputedStyle reports rgb(); the tokens are hex. Normalise rather than
// hard-coding the channel triple, so a palette change does not need a test edit.
function toRgb(hex: string): string {
  const h = hex.replace('#', '')
  const n = parseInt(h.length === 3 ? h.split('').map(c => c + c).join('') : h, 16)
  return `rgb(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255})`
}

function bgOf(disabled: boolean, sx?: object) {
  const { container } = render(
    <ThemeProvider theme={theme}>
      <Button variant="contained" disabled={disabled} sx={sx}>Use emergency access</Button>
    </ThemeProvider>,
  )
  const el = container.querySelector('button') as HTMLButtonElement
  const cs = getComputedStyle(el)
  return { el, bg: cs.backgroundColor, fg: cs.color }
}

describe('disabled contained buttons under the app theme', () => {
  it('does not paint a disabled button like an enabled one', () => {
    const enabled = bgOf(false)
    const disabled = bgOf(true)
    expect(disabled.el.disabled).toBe(true)
    expect(disabled.bg).not.toBe(enabled.bg)
  })

  // The emergency-access card's exact shape: a custom bgcolor via sx, which
  // loses to the theme's three-class override and so cannot rescue the disabled
  // state on its own. Asserted separately so a fix applied only in a view is
  // caught as insufficient.
  it('holds even when the view sets its own bgcolor via sx', () => {
    const custom = { bgcolor: md.tertiary, color: md.onTertiary }
    const enabled = bgOf(false, custom)
    const disabled = bgOf(true, custom)
    expect(disabled.el.disabled).toBe(true)
    expect(disabled.bg).not.toBe(enabled.bg)
  })

  // Pins the disabled look to the app's OWN tokens rather than to MUI's
  // defaults. Without this the suite passes on either half of the fix and
  // cannot tell which one is doing the work; with it, deleting the explicit
  // rule is caught. It also keeps every contained button's disabled state
  // identical — the emergency card paints itself from the tertiary role, and a
  // half-faded tertiary would read as a third state rather than as "off".
  it('uses the MD3 disabled tokens, not MUI defaults', () => {
    const disabled = bgOf(true)
    expect(disabled.bg).toBe(alpha(md.onSurface, 0.12))
    expect(disabled.fg).toBe(alpha(md.onSurface, 0.38))
  })

  // The enabled path must be untouched: deleting the brand override entirely
  // would also make this suite's first two cases pass, while silently
  // restyling every primary button in the app.
  it('leaves the enabled brand colour alone', () => {
    expect(bgOf(false).bg).toBe(toRgb(md.primary))
  })
})
