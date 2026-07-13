function normalize(value: string | undefined): string {
  const path = (value || '').trim().replace(/\/+$/, '')
  return path && path.startsWith('/') ? path : ''
}

// The server writes this meta element into index.html for every request. A
// meta value avoids an inline script, so strict CSP policies remain effective.
const configuredPanelPath = document
  .querySelector<HTMLMetaElement>('meta[name="psp-panel-path"]')
  ?.content

export const panelPath = normalize(configuredPanelPath)

/** Builds a browser-visible panel URL from an app-relative route. */
export function panelURL(route = '/'): string {
  return `${panelPath}/${route.replace(/^\/+/, '')}`.replace(/\/$/, panelPath ? '/' : '/')
}

export const panelAPIBase = `${panelPath}/api`
