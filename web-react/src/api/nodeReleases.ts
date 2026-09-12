import { client } from './client'
import type { NativeInstallArch, NativeInstallMethod, NativeInstallOS } from './servers'

export type NodeReleaseChannel = 'stable' | 'testing'

/** Published official releases accepted by this panel's compatibility catalog. */
export interface NodeRelease {
  version: string
  channel: NodeReleaseChannel
  published_at: string
  release_url: string
  notes: string
  methods: NativeInstallMethod[]
  platforms: { os: NativeInstallOS; arch: NativeInstallArch }[]
}

export interface NodeReleaseCatalog {
  releases: NodeRelease[]
  checked_at: string
}

/** Metadata only: this route never reads or issues a server credential. */
export async function listNodeReleases(signal?: AbortSignal): Promise<NodeReleaseCatalog> {
  const { data } = await client.get<NodeReleaseCatalog>('/admin/servers/node-releases', {
    signal, _skipErrorToast: true,
  })
  return data
}
