import { client } from './client'

export interface VersionInfo {
  version: string
  commit: string
  build_date: string
}

export async function getVersion() {
  const { data } = await client.get<VersionInfo>('/version', { _skipErrorToast: true })
  return data
}
