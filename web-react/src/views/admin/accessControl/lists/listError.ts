// Maps a list's stored refresh error (free text from the server) to a key
// under admin:access_control.lists.error.<key>. Only the shapes the server
// writes are known; anything else is the generic sentence, never the raw text
// (it may carry a URL, an address or a library's wording).
export function listErrorKey(raw: string): { key: 'http' | 'too_large' | 'broad' | 'generic'; params: Record<string, string> } {
  const http = /^fetch: HTTP (\d{3})$/.exec(raw)
  if (http) return { key: 'http', params: { code: http[1] } }
  if (raw === 'dest_list_too_large') return { key: 'too_large', params: {} }
  const broad = /^broad_entry: (.{1,200})$/.exec(raw)
  if (broad) return { key: 'broad', params: { entry: broad[1] } }
  return { key: 'generic', params: {} }
}
