import { prerelease, valid } from 'semver'
import { compareLegacyTag } from './productVersion'
import type { NodeRelease } from '@/api/nodeReleases'
import type { Server } from '@/api/servers'

function canonicalVersion(tag: string): string | undefined {
  // Official tags have one v prefix and no build metadata. Checking the
  // normalized SemVer also rejects loose versions, padding and leading zeroes.
  if (typeof tag !== 'string' || !tag.startsWith('v') || tag.includes('+')) return undefined
  const version = tag.slice(1)
  return valid(version) === version ? version : undefined
}

/** Highest newer reviewed Linux release in the server's saved channel. */
export function newerNodeRelease(
  server: Pick<Server, 'panel_version' | 'update_channel'>,
  releases: NodeRelease[],
): NodeRelease | undefined {
  // The daemon reports either its official tag or that tag plus a Git commit.
  // Development/unknown identities must not be guessed into an upgrade badge.
  const rawIdentity = server.panel_version ?? ''
  const identity = /^(v\S+?)(?: \([0-9a-fA-F]{7,40}\))?$/.exec(rawIdentity)
  if (!identity || identity[0] !== rawIdentity) return undefined
  const current = canonicalVersion(identity[1])
  if (!current) return undefined
  if (server.update_channel !== undefined && server.update_channel !== 'stable' && server.update_channel !== 'beta') return undefined
  const channel = server.update_channel === 'beta' ? 'testing' : 'stable'

  return releases.filter(release => {
    if (!release) return false
    const version = canonicalVersion(release.version)
    if (!version || release.channel !== channel || (prerelease(version) !== null) !== (channel === 'testing')) return false
    if (release.release_url !== `https://github.com/KazuhaHub/Passwall-Node/releases/tag/${release.version}`) return false
    if (!Array.isArray(release.methods) || !release.methods.includes('linux') || !Array.isArray(release.platforms)) return false
    // The remote Linux upgrade recipe detects architecture, so it requires
    // both assets just like the installation/upgrade version selector does.
    if (!['amd64', 'arm64'].every(arch => release.platforms.some(platform => platform?.os === 'linux' && platform.arch === arch))) return false
    // THE PROJECT'S ORDER, NOT SEMVER'S. These are dotless prerelease tags,
    // and SemVer compares the identifier character by character — "beta11"
    // against "beta9" is '1' against '9', so a node on beta9 was told there was
    // no newer release. compareLegacyTag is the same rule the panel's admission
    // check and Passwall Node's own comparator use; this is the third place it
    // is applied, and three implementations of an ordering is two too many.
    return compareLegacyTag(release.version, identity[1]) > 0
  }).sort((left, right) => compareLegacyTag(right.version, left.version))[0]
}
