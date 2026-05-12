// Shared TypeScript types that mirror backend DTOs. Keep in sync with
// internal/transport/http/handler/*.go DTO definitions.

export type Role = 'admin' | 'user'
export type UserSource = 'local' | 'sso'
export type ResetPeriod = 'never' | 'monthly' | 'quarterly'

export interface User {
  id: number
  username: string
  upn?: string
  source: UserSource
  role: Role
  group_id: number
  uuid: string
  sub_url: string
  expire_at?: string | null
  traffic_limit_bytes: number
  traffic_reset_period: ResetPeriod
  remark?: string
  enabled: boolean
  auto_disabled_reason?: string
  created_at: string
}

export interface CreateUserRequest {
  username: string
  password?: string
  group_id: number
  expire_at?: string
  traffic_limit_gb?: number
  traffic_reset_period?: ResetPeriod
  remark?: string
}

export interface CreateUserResponse {
  user: User
  initial_password: string
  synced_inbounds: number
}

export interface ListResponse<T> {
  items: T[]
  total: number
}

export interface TagFilter {
  all: boolean
  tags: string[]
}

export interface Layout {
  separators: { position: number; name: string }[]
  sort: { node_id: number; weight: number }[]
  default_sort_strategy: string
}

export interface Group {
  id: number
  slug: string
  name: string
  tag_filter: TagFilter
  layout: Layout
  remark?: string
  members: number
}

export interface Node {
  id: number
  panel_name: string
  inbound_id: number
  display_name: string
  server_address: string
  region: string
  tags: string[]
  sort_order: number
  enabled: boolean
}

export interface UnmanagedInbound {
  PanelName: string
  InboundID: number
  Protocol: string
  Port: number
  Remark: string
  Enable: boolean
  ClientCount: number
}

export interface AuthLoginResponse {
  access_token: string
  refresh_token: string
  user: {
    id: number
    username: string
    role: Role
    source: UserSource
  }
}
