import { createContext } from 'react'

/**
 * What an InheritanceField hands the editor it renders. Inside it, "empty" is not an
 * answer a box may give: going back to following the level above is done with the mode
 * control and nowhere else, so an editor must not offer its own way back (NumberField
 * hides its "follow again" button and its empty label) and an empty box is simply
 * invalid until it is filled. `false` outside one.
 */
export interface InheritEditorApi {
  /**
   * An editor that cancelled an edit (Escape) and is about to write `back` as the
   * value calls this first. Returns true when the field took care of it (it restored
   * "nothing written yet"), in which case the editor must not write anything itself.
   */
  revert: (back: unknown) => boolean
}

export const InheritEditorContext = createContext<false | InheritEditorApi>(false)

/**
 * Collapsed form sections by their <section> element, so that something that only has
 * an element in hand (a problem to focus) can open every section the element is hidden
 * in. FormSection registers itself here.
 */
export const sectionReveals = new WeakMap<Element, () => void>()
