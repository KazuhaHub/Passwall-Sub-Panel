import { useEffect, useRef, type RefObject } from 'react'

// Containers that can hide their contents (a collapsed form section, an inactive tab that
// stays mounted) register here how to show them again. Something that only has an element
// in hand, such as "take the person to the field that has a problem", then walks up from
// the element and opens everything it is hidden in, without knowing what kinds of
// container there are.
const revealers = new WeakMap<Element, () => void>()

/** The reveal functions of every registered container around `el` (and `el` itself), innermost first. */
export function revealersAround(el: Element): (() => void)[] {
  const out: (() => void)[] = []
  for (let n: Element | null = el; n; n = n.parentElement) {
    const reveal = revealers.get(n)
    if (reveal) out.push(reveal)
  }
  return out
}

/**
 * Register `ref`'s element as a container that can be shown again by `reveal`. `reveal` runs
 * when a problem inside needs to be reached, and should leave the container open for this
 * visit only (it must not overwrite what the person chose to remember).
 */
export function useRevealOnProblem(ref: RefObject<Element | null>, reveal: () => void): void {
  const latest = useRef(reveal)
  latest.current = reveal
  useEffect(() => {
    const el = ref.current
    if (!el) return
    revealers.set(el, () => latest.current())
    return () => { revealers.delete(el) }
  }, [ref])
}
