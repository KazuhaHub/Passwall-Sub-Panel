// Capability layer — the single source of truth for "what can the current
// account do" in the SPA. Views/components check *capabilities*, never raw
// roles, so introducing custom roles (or a backend-provided permission set)
// later means changing only ROLE_CAPS / roleCan here — no call-site edits.
//
// Today capabilities are derived from the three built-in roles. To grow into
// custom RBAC: replace ROLE_CAPS with a map fetched from the backend (e.g.
// keyed by role name) and have roleCan consult it; the Capability union and
// every `useCan(...)` call site stay exactly as they are.

import { useAuthStore } from '@/stores/auth'
import type { Role } from '@/api/types'

export type Capability =
  // Admin-only infrastructure / global config mutations: 3X-UI servers,
  // nodes & separators, groups, rule sets, templates, system/mail/SSO
  // settings, and log purges/clears.
  | 'config.write'
  // Create / edit / delete regular (role=user) accounts.
  | 'users.write'
  // Elevated user actions: assigning admin/operator roles, or modifying
  // admin/operator accounts at all.
  | 'users.elevate'
  // Per-user / per-node traffic edits and emergency-access grants.
  | 'traffic.write'
  // Retry / cancel sync tasks.
  | 'sync.operate'

const ROLE_CAPS: Record<Role, Capability[]> = {
  admin: ['config.write', 'users.write', 'users.elevate', 'traffic.write', 'sync.operate'],
  operator: ['users.write', 'traffic.write', 'sync.operate'],
  user: [],
}

// roleCan is the pure mapping. Empty/unknown role grants nothing.
export function roleCan(role: Role | '', cap: Capability): boolean {
  if (!role) return false
  return (ROLE_CAPS[role] ?? []).includes(cap)
}

// useCan subscribes to the current role and reports whether it grants cap.
// Mirrors the backend's adminGroup vs staffGroup split — keep in sync.
export function useCan(cap: Capability): boolean {
  return useAuthStore(s => roleCan(s.role, cap))
}
