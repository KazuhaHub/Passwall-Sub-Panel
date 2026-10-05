import { client } from './client'
import type { ReadOptions } from './requestOptions'
import type { UISettings } from './settings'

export type AccessControlSettingKey =
  | 'dest_hit_retention_days' | 'dest_trial_retention_days' | 'dest_usage_retention_days'
  | 'dest_list_refresh_hours' | 'dest_policy_apply_min_seconds'

/** Stored zero selects the default returned by the server. */
export type AccessControlSettings = Required<Pick<UISettings, AccessControlSettingKey>>

export interface AccessControlSettingsView {
  settings: AccessControlSettings
  defaults: AccessControlSettings
  effective: AccessControlSettings
}

export async function getAccessControlSettings(opts: ReadOptions = {}): Promise<AccessControlSettingsView> {
  const { data } = await client.get<AccessControlSettingsView>('/admin/dest/settings', {
    signal: opts.signal,
    _skipErrorToast: opts.silent,
  })
  return data
}

/** Send only edited fields so another tab's changes remain intact. */
export async function putAccessControlSettings(changed: Partial<AccessControlSettings>): Promise<AccessControlSettingsView> {
  const { data } = await client.put<AccessControlSettingsView>('/admin/dest/settings', { settings: changed }, {
    _skipErrorToast: true,
  })
  return data
}

export interface DestinationPolicyRetryResult {
  retry_requested: boolean
}

export async function retryDestinationPolicy(agentId: string): Promise<DestinationPolicyRetryResult> {
  const { data } = await client.post<DestinationPolicyRetryResult>(
    `/admin/dest/agents/${encodeURIComponent(agentId)}/retry`, undefined, { _skipErrorToast: true },
  )
  return data
}
