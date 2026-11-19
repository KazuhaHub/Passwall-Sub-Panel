import type { ReactNode } from 'react'
import { alpha, useTheme } from '@mui/material/styles'

// LegacyBoundary marks a subtree as old MUI content that sits inside the new
// shell (.psp-ui / [data-slot] ancestors). styles/ui.css matches new-scope rules
// by NEAREST marker, so nothing in here is touched by the Tailwind reset, the
// border/font defaults or the MUI coexistence shim. Inherited properties,
// however, cross any selector boundary, so this component re-states, from the
// live theme, everything CssBaseline puts on <body> and everything the new scope
// sets that inherits. Without it MUI text would inherit Geist, the shell's
// colours and line-height.
//
// Contract (see ui.css): markers nest at most three deep, new shell -> this
// boundary -> new shared component re-entering. Delete this component, the
// `.psp-legacy` rules and the shim together with MUI.
export default function LegacyBoundary({ children }: { children: ReactNode }) {
  const theme = useTheme()
  const body = theme.typography.body1
  const thumb = alpha(theme.palette.md.onSurfaceVariant, 0.32)
  return (
    <div
      className="psp-legacy"
      style={{
        // CssBaseline body: colour + typography.body1 + background.
        color: theme.palette.text.primary,
        fontFamily: body.fontFamily,
        fontSize: body.fontSize,
        fontWeight: body.fontWeight,
        lineHeight: body.lineHeight,
        letterSpacing: body.letterSpacing,
        backgroundColor: theme.palette.background.default,
        // The theme's `*` scrollbar rule, which the new scope also inherits from.
        scrollbarWidth: 'thin',
        scrollbarColor: `${thumb} transparent`,
        // Inheriting properties the new scope sets (preflight / font rules) and
        // that MUI pages leave at the browser default. `initial` is the default.
        textRendering: 'auto',
        tabSize: 'initial',
        WebkitTapHighlightColor: 'initial',
        WebkitFontSmoothing: 'antialiased', // html in CssBaseline
      }}
    >
      {children}
    </div>
  )
}
