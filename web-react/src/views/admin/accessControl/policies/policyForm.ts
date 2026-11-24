import type { DestAction, DestPolicy, DestPolicyInput, DestScope, DestState } from '@/api/accessControl'

// THE POLICY EDITOR'S FORM MODEL: the form's shape, how a stored policy becomes a form and how a form becomes
// the request body. Pure, so a test can pin the body without rendering the sheet.

export const PREVIEW_DEBOUNCE_MS = 400
/** The server's cap on a description, in UTF-8 bytes. */
export const DESCRIPTION_MAX_BYTES = 512
export const NAME_MAX_BYTES = 128
export const utf8Bytes = (text: string) => new TextEncoder().encode(text).length
/** The error keys a rendered input shows; any other field falls back to the dialog's top error. */
export const RENDERED_FIELDS = new Set(['name', 'description', 'list_ids', 'cidrs', 'ports', 'match', 'group_ids', 'panel_ids', 'scope'])

export interface PolicyForm {
  name: string
  description: string
  action: DestAction
  state: DestState
  listIds: number[]
  cidrs: string
  ports: string
  network: '' | 'tcp' | 'udp'
  bittorrent: boolean
  private: boolean
  scope: DestScope
  groupIds: number[]
  panelIds: number[]
  templateKey: string
  /** Block policies only: the real blocks count as a risk signal. */
  countsAsRisk: boolean
}

/**
 * A new policy's form. A block policy starts as Report only, which the dialog
 * recommends and the admin may change to anything; an allow policy has no
 * report-only and starts Off.
 */
export const emptyForm = (action: DestAction = 'block'): PolicyForm => ({
  name: '', description: '', action, state: action === 'block' ? 'report' : 'off', listIds: [], cidrs: '', ports: '', network: '', bittorrent: false,
  private: false, scope: 'all', groupIds: [], panelIds: [], templateKey: '', countsAsRisk: false,
})

export function formOf(p: DestPolicy): PolicyForm {
  return {
    name: p.name, description: p.description, action: p.action, state: p.state, listIds: p.list_ids,
    cidrs: (p.inline.cidrs ?? []).join('\n'), ports: p.inline.ports ?? '',
    network: p.inline.network === 'tcp' || p.inline.network === 'udp' ? p.inline.network : '',
    bittorrent: (p.inline.protocols ?? []).includes('bittorrent'), private: p.inline.private === true,
    scope: p.scope, groupIds: p.group_ids, panelIds: p.panel_ids, templateKey: p.template_key, countsAsRisk: p.action === 'block' && p.counts_as_risk === true,
  }
}

/** The request body of a form; `updatedAt` only on an edit. */
export function inputOf(f: PolicyForm, extra: { id?: number; updatedAt?: number } = {}): DestPolicyInput {
  const cidrs = f.cidrs.split('\n').map(s => s.trim()).filter(Boolean)
  return {
    name: f.name.trim(), description: f.description.trim(), action: f.action, state: f.state, list_ids: f.listIds,
    inline: {
      ...(cidrs.length ? { cidrs } : {}), ...(f.ports.trim() ? { ports: f.ports.trim() } : {}),
      ...(f.network ? { network: f.network } : {}), ...(f.bittorrent ? { protocols: ['bittorrent'] } : {}),
      ...(f.private ? { private: true } : {}),
    },
    scope: f.scope, group_ids: f.scope === 'groups' ? f.groupIds : [], panel_ids: f.scope === 'panels' ? f.panelIds : [],
    ...(f.templateKey ? { template_key: f.templateKey } : {}),
    // An allow policy sends none: the server clears it on a block turned allow and refuses a true one.
    ...(f.action === 'block' ? { counts_as_risk: f.countsAsRisk } : {}),
    ...(extra.id ? { id: extra.id } : {}), ...(extra.updatedAt !== undefined ? { updated_at: extra.updatedAt } : {}),
  }
}

/** Whether a change reaches the nodes: a rename, a new description or the risk flag (none of them compiled) does not. */
export function changesWire(a: PolicyForm, b: PolicyForm): boolean {
  return JSON.stringify({ ...a, name: '', description: '', countsAsRisk: false }) !== JSON.stringify({ ...b, name: '', description: '', countsAsRisk: false })
}

export const lineNumbersOf = (text: string) => text.split('\n').map((l, i) => [l.trim(), i + 1] as const).filter(([l]) => l)
