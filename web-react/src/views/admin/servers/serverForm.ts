import type { CreateServerRequest, PanelType, Server, UpdateServerRequest, XUIAuthMethod, NodeUpdateChannel } from '@/api/servers'
import { effectiveUpdateChannel } from '@/utils/nodeReleaseUpdate'
import { validateName, validateRequired, validateUrl } from '@/utils/validators'

/**
 * The add / edit server form, as data: its state, its rules and the two request bodies. Pure on purpose, so the
 * rules that decide what is sent (and what is NOT sent) can be read and tested without a page around them.
 */
export interface ServerForm {
  panel_type: PanelType
  update_channel: NodeUpdateChannel
  name: string
  url: string
  /** A secret is never prefilled. Empty on an edit means "keep the stored one"; typing replaces it. */
  api_token: string
  username: string
  password: string
  remark: string
  region: string
  tags: string[]
  auth_method: XUIAuthMethod
  insecure_https: boolean
}

export type ServerField = 'name' | 'url' | 'api_token' | 'password'
/** A key of the admin namespace per field with a problem. */
export type ServerFieldErrors = Partial<Record<ServerField, string>>

export const EMPTY_FORM: ServerForm = {
  name: '', url: '', api_token: '', username: '', password: '', remark: '', region: '', tags: [],
  panel_type: 'psp', update_channel: 'stable', auth_method: 'token', insecure_https: false,
}

export function formOf(s: Server): ServerForm {
  return {
    ...EMPTY_FORM,
    name: s.name,
    panel_type: s.panel_type ?? '3xui',
    update_channel: effectiveUpdateChannel(s.update_channel),
    url: s.url,
    username: s.username ?? '',
    remark: s.remark ?? '',
    region: s.region ?? '',
    tags: s.tags ?? [],
    auth_method: s.auth_method ?? 'token',
    insecure_https: !!s.insecure_https,
  }
}

/** What changing the type does to the rest of the form (only a new server can change type). */
export function withPanelType(prev: ServerForm, panelType: PanelType): ServerForm {
  // Choosing the type already chosen is not a change: it must not reset fields (a Passwall Node blanks auth_method)
  // and so make an untouched form look edited, which would ask to discard it on Cancel.
  if (panelType === prev.panel_type) return prev
  if (panelType === 'sui') return { ...prev, panel_type: panelType, auth_method: 'token', username: '', password: '' }
  if (panelType === 'psp') return { ...prev, panel_type: panelType, url: '', api_token: '', username: '', password: '', auth_method: '', insecure_https: false }
  return { ...prev, panel_type: panelType, auth_method: prev.auth_method || 'token' }
}

export function tokenRequired(f: ServerForm): boolean {
  return f.panel_type === 'sui' || f.auth_method === 'token'
}

/**
 * The rules, in one place. A Passwall Node has no address or credentials to check. For the others the credential
 * the chosen auth method reads is required unless one is already stored (an edit leaves it empty to keep it).
 * S-UI exposes only token-authenticated /apiv2, so a stale 3X-UI password must never make the form look valid after
 * switching the type.
 */
export function validateServerForm(f: ServerForm, editing: Server | null): ServerFieldErrors {
  const name = validateName(f.name, { required: true, max: 64 })
  if (f.panel_type === 'psp') return name ? { name } : {}
  const needToken = tokenRequired(f)
  const out: ServerFieldErrors = {}
  if (name) out.name = name
  const url = validateUrl(f.url, { required: true })
  if (url) out.url = url
  if (needToken && !f.api_token && !editing?.has_api_token) out.api_token = validateRequired('', 'validation.required')
  if (!needToken && !f.password && !editing?.has_password) out.password = validateRequired('', 'validation.required')
  return out
}

/** Region and tags are sent only when they differ from what the server has, so an untouched label is never rewritten. */
export function labelChanges(f: ServerForm, editing: Server | null): { region?: string; tags?: string[] } {
  const region = f.region.trim()
  const tags = f.tags.map(tag => tag.trim()).filter(Boolean)
  return {
    ...((editing?.region ?? '') !== region ? { region } : {}),
    ...(JSON.stringify(editing?.tags ?? []) !== JSON.stringify(tags) ? { tags } : {}),
  }
}

export function updateRequest(editing: Server, f: ServerForm): UpdateServerRequest {
  const labels = labelChanges(f, editing)
  if (editing.panel_type === 'psp') return { name: f.name, remark: f.remark, update_channel: f.update_channel, ...labels }
  const req: UpdateServerRequest = {
    panel_type: f.panel_type, url: f.url, name: f.name, username: f.username, remark: f.remark,
    auth_method: f.auth_method, insecure_https: f.insecure_https,
  }
  // A stored secret is replaced only by typing a new one: the box is never prefilled, so empty means keep.
  if (f.api_token) req.api_token = f.api_token
  if (f.password) req.password = f.password
  return { ...req, ...labels }
}

export function createRequest(f: ServerForm): CreateServerRequest {
  const labels = labelChanges(f, null)
  if (f.panel_type === 'psp') return { ...labels, name: f.name, panel_type: 'psp', remark: f.remark || undefined, update_channel: f.update_channel }
  return {
    ...labels, name: f.name, url: f.url, panel_type: f.panel_type,
    api_token: f.api_token || undefined, username: f.username || undefined, password: f.password || undefined,
    remark: f.remark || undefined, auth_method: f.auth_method, insecure_https: f.insecure_https,
  }
}

export function sameForm(a: ServerForm, b: ServerForm): boolean {
  return JSON.stringify(a) === JSON.stringify(b)
}
