import type { DestinationPolicyInput } from '@/api/accessControl'

export function policyInput(policy: DestinationPolicyInput): DestinationPolicyInput {
  return { name: policy.name, action: policy.action, list_ids: [...policy.list_ids], inline: structuredClone(policy.inline),
    scope: policy.scope, group_ids: [...policy.group_ids], enabled: policy.enabled,
    counts_as_risk: policy.action === 'block' && policy.counts_as_risk, template_key: policy.template_key }
}
export const emptyPolicy = (): DestinationPolicyInput => ({ name: '', action: 'block', list_ids: [], inline: {}, scope: 'all', group_ids: [], enabled: false, counts_as_risk: false, template_key: '' })
export function isCIDR(value: string): boolean {
  const parts = value.split('/')
  if (parts.length !== 2 || !/^\d+$/.test(parts[1])) return false
  const [address, prefix] = parts
  if (address.includes(':')) {
    if (!/^[\da-fA-F:.]+$/.test(address) || Number(prefix) > 128) return false
    try { return new URL(`http://[${address}]/`).hostname.startsWith('[') } catch { return false }
  }
  return Number(prefix) <= 32 && /^(?:0|[1-9]\d{0,2})(?:\.(?:0|[1-9]\d{0,2})){3}$/.test(address) && address.split('.').every(part => Number(part) <= 255)
}
export function validatePolicyDraft(input: DestinationPolicyInput): Record<string, string> {
  const errors: Record<string, string> = {}
  if (!input.name.trim()) errors.name = 'required'
  const { ports, cidrs = [], protocols = [], private: privateIP } = input.inline
  if (!input.list_ids.length && !ports?.trim() && !cidrs.some(line => line.trim()) && !protocols.length && !privateIP) errors.match = 'no_match'
  if (input.scope === 'groups' && !input.group_ids.length) errors.group_ids = 'groups_required'
  if (ports && !ports.split(',').every(part => {
    if (!/^\d+(?:-\d+)?$/.test(part)) return false
    const [start, end = start] = part.split('-').map(Number)
    return start >= 1 && end <= 65535 && end >= start
  })) errors.ports = 'ports_invalid'
  const badLines = cidrs.flatMap((line, index) => line.trim() && !isCIDR(line.trim()) ? [index + 1] : [])
  if (badLines.length) errors.cidrs = badLines.join(', ')
  return errors
}
export function matchSummary(input: DestinationPolicyInput) {
  const cidrs = (input.inline.cidrs ?? []).filter(line => line.trim()).length
  const bt = input.inline.protocols?.includes('bittorrent') ?? false
  return { list_ids: input.list_ids, ports: input.inline.ports ?? '', network: input.inline.network ?? '', cidrs,
    bt, private: input.inline.private ?? false, split: bt && (input.list_ids.length > 0 || cidrs > 0 || !!input.inline.private) }
}
export function executionChanged(next: DestinationPolicyInput, seed: DestinationPolicyInput): boolean {
  const execution = (value: DestinationPolicyInput) => ({ action: value.action, list_ids: [...value.list_ids].sort((a,b) => a-b), inline: value.inline, scope: value.scope, group_ids: [...value.group_ids].sort((a,b) => a-b), enabled: value.enabled })
  return JSON.stringify(execution(next)) !== JSON.stringify(execution(seed))
}
