/**
 * The addresses of the Servers section. A server is a page (`/admin/servers/7`), adding one another
 * (`/admin/servers/new`), and a Passwall Node's installation a third (`/admin/servers/7/install`). The list keeps its
 * own place in its query string.
 */
export const SERVERS_PATH = '/admin/servers'
export const NEW_SERVER_PATH = `${SERVERS_PATH}/new`

export const serverPath = (id: number) => `${SERVERS_PATH}/${id}`
export const serverInstallPath = (id: number) => `${SERVERS_PATH}/${id}/install`

/**
 * The id in an address, or 0 for anything that is not a positive whole number. `new` is its own route and never reaches
 * a page that parses an id; a malformed address is "no such server" without a request.
 */
export function parseServerId(raw: string | undefined): number {
  if (!/^\d+$/.test(raw ?? '')) return 0
  const n = Number(raw)
  return Number.isSafeInteger(n) && n > 0 ? n : 0
}
