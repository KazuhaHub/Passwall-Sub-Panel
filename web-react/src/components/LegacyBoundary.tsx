import { useEffect, useRef, type CSSProperties, type ReactNode } from 'react'
import { useTheme } from '@mui/material/styles'
import { NEW_SCOPE_LEVELS, levelNeeded } from '../../scripts/psp-scope-chain.mjs'

// LegacyBoundary marks a subtree as old MUI content inside new-stack markup. The
// new scope is decided by the NEAREST marker (see styles/ui.css and
// scripts/psp-scope.mjs), so nothing in here is touched by the Tailwind reset,
// the border/font defaults or the MUI coexistence shim, however deep the
// nesting: shell > legacy page > new component > legacy slot > new component ...
//
// WHO USES IT. The shell wraps its page outlet in it. A NEW component that
// accepts arbitrary children or slots from a not-yet-migrated caller (a
// PageHeader's `actions`, a dialog body, a card's content) wraps that slot in it
// too: the new component is responsible for the boundary, during the migration.
// Once every caller is migrated the wrapper is deleted.
//
// WHAT IT DOES. It is layout-neutral (`display: contents`: no box, so a
// `height: 100%` page inside a flex column behaves as if it were not there) and
// paints nothing. Inherited properties still pass through a `display: contents`
// element, so it re-states, from the live theme, everything CssBaseline puts on
// <body> that inherits, plus what the new scope sets on its entry points that
// inherits (font, colour, tab size, tap highlight, text rendering). Without that,
// MUI text inside the shell would inherit Geist and the shell's colours.
// `className` / `style` are passed through; a caller that needs the M3 surface
// behind a legacy page sets `style={{ display: 'block', backgroundColor: ... }}`.
//
// Delete this component, the `.psp-legacy` rules and the shim together with MUI.

// The marker chain from the root down to `el`, e.g. 'ULU' (see psp-scope-chain).
export function chainOf(el: Element): string {
  let chain = ''
  for (let n: Element | null = el; n; n = n.parentElement) {
    if (n.classList.contains('psp-legacy')) chain = 'L' + chain
    else if (n.classList.contains('psp-ui') || n.hasAttribute('data-slot')) chain = 'U' + chain
  }
  return chain
}

export default function LegacyBoundary({ children, className, style }: {
  children: ReactNode
  className?: string
  style?: CSSProperties
}) {
  const theme = useTheme()
  const body = theme.typography.body1
  const ref = useRef<HTMLDivElement>(null)

  // Development only (dead code in production builds): a new component that
  // re-enters below this boundary needs selector level levelNeeded(chain + 'U').
  // The generated selector supports NEW_SCOPE_LEVELS levels; beyond that such a
  // component would silently lose its reset, so say so loudly.
  useEffect(() => {
    if (!import.meta.env.DEV || !ref.current) return
    const need = levelNeeded(chainOf(ref.current) + 'U')
    if (need > NEW_SCOPE_LEVELS - 1) {
      console.error(
        `LegacyBoundary: marker nesting is too deep. A new component below this .psp-legacy needs selector level ${need}, `
        + `but only ${NEW_SCOPE_LEVELS - 1} is generated (scripts/psp-scope-chain.mjs NEW_SCOPE_LEVELS). `
        + 'Flatten the nesting or raise the constant.',
      )
    }
  })

  return (
    <div
      ref={ref}
      className={className ? `psp-legacy ${className}` : 'psp-legacy'}
      style={{
        display: 'contents',
        // CssBaseline body: colour + typography.body1.
        color: theme.palette.text.primary,
        fontFamily: body.fontFamily,
        fontSize: body.fontSize,
        fontWeight: body.fontWeight,
        lineHeight: body.lineHeight,
        letterSpacing: body.letterSpacing,
        // Inheriting properties the new scope sets on its entry points and that
        // MUI pages leave at the browser default. `initial` is that default.
        textRendering: 'auto',
        tabSize: 'initial',
        WebkitTapHighlightColor: 'initial',
        WebkitFontSmoothing: 'antialiased', // html in CssBaseline
        ...style,
      }}
    >
      {children}
    </div>
  )
}
