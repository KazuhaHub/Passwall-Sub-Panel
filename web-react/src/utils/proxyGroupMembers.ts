import type { LoadBalanceStrategy, ProxyGroupMember, ProxyGroupOptions, ProxyGroupType } from '@/api/rules'

export const DEFAULT_PROXY_GROUP_TEST_URL = 'https://www.gstatic.com/generate_204'
export const DEFAULT_PROXY_GROUP_INTERVAL = 300
export const DEFAULT_PROXY_GROUP_TIMEOUT = 5000
export const DEFAULT_PROXY_GROUP_TOLERANCE = 50
export const DEFAULT_LOAD_BALANCE_STRATEGY: LoadBalanceStrategy = 'consistent-hashing'
export const DEFAULT_PROXY_GROUP_ORDER = [
  '🚀 节点选择',
  '🎮 UDP控制',
  '🇨🇳 中国大陆',
  '💬 Ai平台',
  '📹 油管视频',
  '🎥 奈飞视频',
  '📺 巴哈姆特',
  '🌍 国外媒体',
  '🎮 游戏平台',
  '📲 电报消息',
  'Ⓜ️ 微软Bing',
  '📢 谷歌FCM',
  '🌏 国内媒体',
  '📺 哔哩哔哩',
  'Ⓜ️ 微软云盘',
  'Ⓜ️ 微软服务',
  '🍎 苹果服务',
  '🎶 网易音乐',
  '🎯 全球直连',
  '🛑 广告拦截',
  '🍃 应用净化',
  '🐟 漏网之鱼',
]

export function defaultProxyGroupOptions(type: ProxyGroupType): ProxyGroupOptions {
  if (type === 'select') return { type }
  const common: ProxyGroupOptions = {
    type,
    url: DEFAULT_PROXY_GROUP_TEST_URL,
    interval: DEFAULT_PROXY_GROUP_INTERVAL,
    lazy: true,
    timeout: DEFAULT_PROXY_GROUP_TIMEOUT,
  }
  if (type === 'url-test') common.tolerance = DEFAULT_PROXY_GROUP_TOLERANCE
  if (type === 'load-balance') common.strategy = DEFAULT_LOAD_BALANCE_STRATEGY
  return common
}

export function proxyGroupOptionsEqual(left?: ProxyGroupOptions, right?: ProxyGroupOptions): boolean {
  if (left === undefined || right === undefined) return left === right
  return left.type === right.type && left.url === right.url && left.interval === right.interval &&
    left.lazy === right.lazy && left.timeout === right.timeout && left.tolerance === right.tolerance && left.strategy === right.strategy
}

export function applyProxyGroupOrder<T extends { name: string }>(groups: T[], preferredOrder: string[]): T[] {
  const effectiveOrder = preferredOrder.length ? preferredOrder : DEFAULT_PROXY_GROUP_ORDER
  const byName = new Map(groups.map(group => [group.name, group]))
  const ordered: T[] = []
  const preferredNames = new Set(effectiveOrder)
  for (const group of groups) {
    if (preferredNames.has(group.name) || !byName.delete(group.name)) continue
    ordered.push(group)
  }
  for (const name of effectiveOrder) {
    const group = byName.get(name)
    if (!group) continue
    ordered.push(group)
    byName.delete(name)
  }
  return ordered
}

export function reorderProxyGroupNames(names: string[], from: number, to: number): string[] {
  if (from === to || from < 0 || to < 0 || from >= names.length || to >= names.length) return names
  const next = [...names]
  const [name] = next.splice(from, 1)
  next.splice(to, 0, name)
  return next
}

export function proxyGroupOrderEqual(left: string[], right: string[]): boolean {
  return left.length === right.length && left.every((name, index) => name === right[index])
}

export function proxyGroupMemberIdentity(member: ProxyGroupMember): string {
  return `${member.kind}:${member.value || ''}:${member.node_id || 0}`
}

export function reorderProxyGroupMembers(members: ProxyGroupMember[], from: number, to: number): ProxyGroupMember[] {
  if (from === to || from < 0 || to < 0 || from >= members.length || to >= members.length) return members
  const next = [...members]
  const [item] = next.splice(from, 1)
  next.splice(to, 0, item)
  return next
}

export function appendUniqueProxyGroupMember(members: ProxyGroupMember[], member: ProxyGroupMember): ProxyGroupMember[] {
  const identity = proxyGroupMemberIdentity(member)
  if (members.some(existing => proxyGroupMemberIdentity(existing) === identity)) return members
  return [...members, member]
}

export function proxyGroupMemberListsEqual(left?: ProxyGroupMember[], right?: ProxyGroupMember[]): boolean {
  if (left === undefined || right === undefined) return left === right
  return left.length === right.length && left.every((member, index) => proxyGroupMemberIdentity(member) === proxyGroupMemberIdentity(right[index]))
}
