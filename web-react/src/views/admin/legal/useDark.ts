import { useEffect, useState } from 'react'

/** Whether the app is in dark mode now (the `dark` class on <html>), for the one embedded editor that draws its own theme. */
export function useDark(): boolean {
  const read = () => typeof document !== 'undefined' && document.documentElement.classList.contains('dark')
  const [dark, setDark] = useState(read)
  useEffect(() => {
    const observer = new MutationObserver(() => setDark(read()))
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })
    return () => observer.disconnect()
  }, [])
  return dark
}
