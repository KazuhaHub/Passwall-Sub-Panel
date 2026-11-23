import type { NativeInstallationSelection, NodeUpdateChannel } from '@/api/servers'
import { effectiveUpdateChannel } from '@/utils/nodeReleaseUpdate'
import { canonicalReleaseVersion, versionOfTag } from '@/utils/productVersion'

/** What the installation starts from: the Linux one-line installer for the host architecture. */
export const DEFAULT_INSTALLATION: NativeInstallationSelection = {
  method: 'linux', os: 'linux', arch: 'amd64', dockerRemoteUpgrade: true,
}

const PUBLIC_NODE_INSTALL_BASE_COMMAND = "curl --disable --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --tlsv1.2 https://raw.githubusercontent.com/KazuhaHub/Passwall-Node/main/install.sh | sudo sh"

/** The public (secret-free) installer commands for a release channel. The credential never appears in them. */
export function publicNodeInstallCommands(channel: NodeUpdateChannel) {
  const canonical = effectiveUpdateChannel(channel)
  const args = canonical === 'stable' ? [] : [`--channel ${canonical}`]
  const command = (extra: string[]) => extra.length
    ? `${PUBLIC_NODE_INSTALL_BASE_COMMAND} -s -- ${extra.join(' ')}`
    : PUBLIC_NODE_INSTALL_BASE_COMMAND
  return { install: command(args), installOnly: command([...args, '--install-only']) }
}
export const PUBLIC_NODE_INSTALL_COMMAND = publicNodeInstallCommands('stable').install
export const PUBLIC_NODE_INSTALL_ONLY_COMMAND = publicNodeInstallCommands('stable').installOnly

// Whether the string names a release this project publishes, in either scheme.
//
// THIS USED TO BE A FOURTH COPY of the version-shape rule, and it knew only the
// legacy one — so a product version left the install action disabled with
// nothing said about why, which reads as "this release cannot be installed"
// rather than "this build does not recognise it". The rule lives in the module
// that reads the shared vectors; this asks it.
export function isNodeReleaseVersion(version: string): boolean {
  return canonicalReleaseVersion(version) !== undefined
}

export function isNodeDockerImageSelection(version: string): boolean {
  return ['stable', 'testing', 'latest', 'beta'].includes(version) || isNodeReleaseVersion(version)
}

// THE DOWNLOAD PATH IS ADDRESSED BY THE TAG AND THE ASSET IS NAMED BY THE
// VERSION, which is what the publisher does. Rebuilding the path from the
// version left every product release unusable: the response was rejected as
// though it named some other file, and the operator was told the release had no
// installation assets.
//
// THE TAG IN THE PATH MUST NAME THE VERSION THAT WAS ASKED FOR, AND EITHER ADDRESS
// DOES. Four releases are published under the historical namespace and everything
// since under the current one, and this side does not know which of the two a given
// version went out under — the panel does, and it states the tag where getting it
// wrong is visible (the release page link). What this has to catch is a path built
// from the VERSION, which names no release in either namespace.
export function isOfficialNodeReleaseDownload(download: { name: string; url: string }, version: string): boolean {
  if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$/.test(download.name)) return false
  const expected = canonicalReleaseVersion(version)
  if (expected === undefined) return false
  const prefix = 'https://github.com/KazuhaHub/Passwall-Node/releases/download/'
  if (!download.url.startsWith(prefix) || !download.url.endsWith('/' + download.name)) return false
  const tag = download.url.slice(prefix.length, download.url.length - download.name.length - 1)
  return versionOfTag(tag) === expected
}

export function installationErrorMessage(error: unknown, fallback: string): string {
  const err = error as { response?: { data?: unknown }; message?: string } | null | undefined
  let data = err?.response?.data
  if (typeof data === 'string') {
    try { data = JSON.parse(data) as unknown } catch { return fallback }
  }
  if (data && typeof data === 'object' && 'error' in data && typeof data.error === 'string') return data.error
  return err?.message ?? fallback
}

export function isCredentialUnavailable(error: unknown): boolean {
  const err = error as { response?: { status?: number; data?: { code?: string } } } | null | undefined
  return err?.response?.status === 409 && err.response.data?.code === 'node_credential_unavailable'
}

export function downloadInstallationFile(name: string, content: string, type = 'text/plain;charset=utf-8') {
  const url = URL.createObjectURL(new Blob([content], { type }))
  try {
    const link = document.createElement('a')
    link.href = url
    link.download = name
    document.body.appendChild(link)
    link.click()
    link.remove()
  } finally { URL.revokeObjectURL(url) }
}
