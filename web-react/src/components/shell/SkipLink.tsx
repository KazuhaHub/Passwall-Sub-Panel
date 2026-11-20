import { useTranslation } from 'react-i18next'

export const MAIN_ID = 'main-content'

/**
 * "Skip to the content", as a button that moves focus itself. It is deliberately NOT a
 * `#main-content` link: the production server injects `<base href="<panel path>/">` into
 * index.html, and a fragment link resolves against that base, so following it navigated to
 * the panel's home page (losing whatever was typed). Nothing here touches the location.
 * It is visible only while focused, the first stop in tab order.
 */
export function SkipLink() {
  const { t } = useTranslation('common')
  return (
    <button
      type="button"
      onClick={() => {
        const main = document.getElementById(MAIN_ID)
        if (!main) return
        // The target is a tabindex=-1 region, so it can take focus without joining the tab order.
        main.focus()
        // Focus scrolls it into view already in a browser; this makes it explicit (and the
        // stylesheet's scroll-padding keeps it clear of the sticky top bar).
        main.scrollIntoView?.({ block: 'start' })
      }}
      className="sr-only z-50 rounded-md bg-background px-3 py-2 text-sm font-medium shadow-md ring-1 ring-foreground/10 focus:not-sr-only focus:fixed focus:top-2 focus:left-2"
    >
      {t('ui.shell.skip_to_content')}
    </button>
  )
}
