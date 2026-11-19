// The "new scope" selector, written once.
//
// An element is in NEW scope when its nearest marker is new. Markers are
// `.psp-ui` / `[data-slot]` (new) and `.psp-legacy` (old MUI content inside a new
// ancestor). By construction they nest at most three deep: new shell -> legacy
// page -> new shared component re-entering. Without `@scope` (older iOS Safari
// lacks it) that is four selectors:
//   1. a new marker that is not, and is not inside, a legacy one
//   2. its descendants that are not, and are not inside, a legacy one
//   3. a new marker inside a legacy one (the re-entry)
//   4. that marker's descendants
// ui.css writes `:psp-new` (zero specificity, for the layered reset),
// `:psp-new-root` (the markers only, for inheriting properties) and
// `:psp-new-strong` (specificity of its strongest member, for the unlayered
// shim); vite.config.ts expands them through this module.
const NEW = '.psp-ui, [data-slot]'
const LEGACY = '.psp-legacy'

export const NEW_SCOPE_MEMBERS = [
  `:is(${NEW}):not(${LEGACY}, ${LEGACY} *)`,
  `:is(${NEW}) :not(${LEGACY}, ${LEGACY} *)`,
  `${LEGACY} :is(${NEW})`,
  `${LEGACY} :is(${NEW}) *`,
]

// The markers themselves (members 1 and 3): where inheriting properties are set.
export const NEW_ROOT_MEMBERS = [NEW_SCOPE_MEMBERS[0], NEW_SCOPE_MEMBERS[2]]

export function expandScope(css) {
  const scope = NEW_SCOPE_MEMBERS.join(', ')
  const roots = NEW_ROOT_MEMBERS.join(', ')
  return css
    .replace(/:psp-new-strong\b/g, `:is(${scope})`)
    .replace(/:psp-new-root\b/g, `:where(${roots})`)
    .replace(/:psp-new\b/g, `:where(${scope})`)
}
