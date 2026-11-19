import React from 'react'
import ReactDOM from 'react-dom/client'
import '@fontsource/roboto/400.css'
import '@fontsource/roboto/500.css'
// @fontsource/noto-sans-sc was previously imported here, but every weight
// pulled in ~196 woff/woff2 unicode-range subsets — 392 font files total
// shipped in dist, and a 260KB CSS file with ~400 @font-face declarations
// parsed on every page load. The theme's font-family stack already
// covers CJK rendering via system fonts (PingFang SC on macOS,
// Microsoft YaHei on Windows, Hiragino Sans on iOS) which all Chinese-
// reading platforms ship with. Re-add a custom font-subset here if a
// future deployment specifically needs Noto Sans SC.
// Tailwind/shadcn stylesheet: global preflight, tokens and fonts (styles/ui.css).
// Import order does not matter for MUI: emotion's styles are unlayered, and an
// unlayered rule beats a layered one whatever the order.
import '@/styles/ui.css'
import i18n, { i18nReady } from '@/i18n'
import App from '@/App'

// Expose i18n on window in dev for quick console-based debugging:
// i18next.hasResourceBundle('zh-CN', 'admin'); i18next.t('admin:servers.title')
if (import.meta.env.DEV) {
  ;(window as unknown as { i18next: typeof i18n }).i18next = i18n
}

// Wait for i18n init to settle before mounting React. Otherwise the first
// render can fire while resources aren't yet registered, leaving every t()
// call to return its key (e.g. "servers.title" instead of "服务器（3X-UI）").
void i18nReady.then(() => {
  ReactDOM.createRoot(document.getElementById('root')!).render(
    <React.StrictMode>
      <App />
    </React.StrictMode>,
  )
})
