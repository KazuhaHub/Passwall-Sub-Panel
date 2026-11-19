import { createContext } from 'react'

/**
 * Set by InheritanceField around the editor it renders. Inside it, "empty" is not an
 * answer a box may give: going back to following the level above is done with the mode
 * control and nowhere else, so an editor must not offer its own way back (NumberField
 * hides its "follow again" button and its empty label) and an empty box is simply
 * invalid until it is filled.
 */
export const InheritEditorContext = createContext(false)
