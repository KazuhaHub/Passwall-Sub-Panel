import React from 'react'
import ReactDOM from 'react-dom/client'
// Tailwind/shadcn stylesheet: global preflight, tokens and fonts (styles/ui.css). The font
// stack covers CJK through system fonts (PingFang SC, Microsoft YaHei, Hiragino Sans), which
// every Chinese-reading desktop platform ships; no CJK font is bundled. A deployment that
// needs one can add a font subset to ui.css.
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
