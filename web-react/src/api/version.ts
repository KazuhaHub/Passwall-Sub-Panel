import { client } from './client'

export interface VersionInfo {
  version: string
  channel?: 'stable' | 'rc' | 'beta' | 'pending' | 'testing' | 'unknown'
  commit: string
  build_date: string
}

export async function getVersion() {
  const { data } = await client.get<VersionInfo>('/version', { _skipErrorToast: true })
  return data
}
