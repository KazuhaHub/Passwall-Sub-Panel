import { prerelease } from 'semver'
import { canonicalReleaseVersion, compareLegacyTag, releaseTag } from './productVersion'
import type { NodeRelease } from '@/api/nodeReleases'
import type { Server } from '@/api/servers'

/**
 * The version a daemon identity or a catalog entry names.
 *
 * IT IS A VERSION, IN EITHER SCHEME, and requiring the v — which is what this
 * did — hid every product release: the daemon is stamped with the release
 * VERSION, so a product build reports `4.0.0` with no prefix at all. The result
 * was not an error but an absence: no release was ever newer than a current
 * version that could not be parsed, so the upgrade badge never appeared.
 *
 * The shape rule lives with the rest of the identity rules, in the module that
 * reads the shared vectors, rather than being a second reading here.
 */
function canonicalVersion(identity: string): string | undefined {
  return canonicalReleaseVersion(identity)
}

/**
 * The channel the release METADATA states, cross-checked against the text only
 * where the text can carry it.
 *
 * A legacy version spells its channel in a prerelease suffix, so the two must
 * agree — an older beta cut before the workflow began setting the flag arrives
 * flagged stable, and the suffix is what corrects it. A product version has no
 * suffix, because a candidate is distinguished by the channel it was published
 * to, so there is nothing to agree with, and requiring agreement hid every
 * product release from the only node supposed to see a testing one.
 */
function channelAgrees(version: string, channel: 'stable' | 'testing'): boolean {
  if (!version.startsWith('v')) return true
  return (prerelease(version) !== null) === (channel === 'testing')
}

/** Highest newer reviewed Linux release in the server's saved channel. */
export function newerNodeRelease(
  server: Pick<Server, 'panel_version' | 'update_channel'>,
  releases: NodeRelease[],
): NodeRelease | undefined {
  // The daemon reports its version, optionally followed by the commit it was
  // built from. Development/unknown identities must not be guessed into an
  // upgrade badge — but WHICH SHAPES ARE OFFICIAL IS NOT DECIDED HERE: the
  // pattern takes anything non-spaced and canonicalVersion is the one rule that
  // says whether it is a version. Requiring a v in the pattern as well put the
  // shape decision in two places, and the copy here was the one that was wrong.
  const rawIdentity = server.panel_version ?? ''
  const identity = /^(\S+?)(?: \([0-9a-fA-F]{7,40}\))?$/.exec(rawIdentity)
  if (!identity || identity[0] !== rawIdentity) return undefined
  const current = canonicalVersion(identity[1])
  if (!current) return undefined
  if (server.update_channel !== undefined && server.update_channel !== 'stable' && server.update_channel !== 'beta') return undefined
  const channel = server.update_channel === 'beta' ? 'testing' : 'stable'

  return releases.filter(release => {
    if (!release) return false
    const version = canonicalVersion(release.version)
    if (!version || release.channel !== channel || !channelAgrees(version, channel)) return false
    // THE RELEASE PAGE IS ADDRESSED BY THE TAG. A product release lives at
    // `tag/release/4.0.0` while its version is `4.0.0`, so rebuilding the URL
    // from the version asks for a page that does not exist and drops the
    // release — silently, because a release that fails a check is skipped.
    const tag = releaseTag(release.version, release.release_tag)
    if (!tag || release.release_url !== `https://github.com/KazuhaHub/Passwall-Node/releases/tag/${tag}`) return false
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
