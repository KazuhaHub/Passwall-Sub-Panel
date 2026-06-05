// formatDualTz renders a timestamp in the panel timezone first (the "system
// view" the panel reports against — traffic resets, expiry, etc.) with the
// browser-local rendering in parentheses. Falls back to a single value when the
// two timezones are identical or the panel tz is unset. Shared by the Logs and
// Certificates pages so every date renders the same way.
export function formatDualTz(s: string | undefined | null, panelTz: string): string {
  if (!s) return '-'
  const d = new Date(s)
  if (Number.isNaN(d.getTime())) return '-'
  let bz = ''
  try {
    bz = Intl.DateTimeFormat().resolvedOptions().timeZone
  } catch {
    bz = ''
  }
  const panelStr = panelTz ? d.toLocaleString(undefined, { timeZone: panelTz }) : d.toLocaleString()
  if (!panelTz || panelTz === bz) return panelStr
  const browserStr = d.toLocaleString()
  return `${panelStr} (${browserStr})`
}
