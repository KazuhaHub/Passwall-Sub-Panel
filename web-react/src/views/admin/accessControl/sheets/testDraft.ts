import type { DestinationTestInput } from '@/api/accessControl'

export function testTarget(raw: string): { target: string | null; url: boolean; ip: boolean } {
  const empty = { target: null, url: false, ip: false }
  raw = raw.trim()
  if (!raw || new TextEncoder().encode(raw).length > 4096 || /[\r\n\t]/.test(raw)) return empty
  let host = raw, url = false
  if (raw.includes('://')) {
    try {
      const parsed = new URL(raw)
      if (!['http:', 'https:'].includes(parsed.protocol) || parsed.username || parsed.password || !parsed.hostname || /[\\]/.test(raw)) return empty
      host = parsed.hostname.replace(/^\[|\]$/g, ''); url = true
    } catch { return empty }
  }
  if (host.includes(':')) {
    try {
      if (!/^[a-f\d:.]+$/i.test(host)) return empty
      new URL(`http://[${host}]/`)
      return { target: host.toLowerCase(), url, ip: true }
    } catch { return empty }
  }
  host = host.toLowerCase().replace(/\.$/, '')
  if (/[^\x00-\x7f]/.test(host)) {
    try { host = new URL(`http://${host}/`).hostname } catch { return empty }
  }
  if (host.length > 253 || !host.split('.').every(label => label.length > 0 && label.length <= 63 && /^[a-z\d](?:[a-z\d-]*[a-z\d])?$/.test(label))) return empty
  const ip = /^\d+\.\d+\.\d+\.\d+$/.test(host) && host.split('.').every(part => Number(part) <= 255 && (part === '0' || !part.startsWith('0')))
  return { target: host, url, ip }
}
export function testDraftInput(target: string, port: string, network: 'tcp' | 'udp', userId: number | null, panelId: number | null): DestinationTestInput | null {
  const normalized = testTarget(target)
  if (!normalized.target || !/^\d+$/.test(port) || Number(port) < 1 || Number(port) > 65535) return null
  return { target: normalized.target, port: Number(port), network, ...(userId && userId > 0 ? { user_id: userId } : {}), ...(panelId && panelId > 0 ? { panel_id: panelId } : {}) }
}
