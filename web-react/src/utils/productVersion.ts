// The identity rules for a Passwall product release, on the client side.
//
// This file is one of three consumers of releaseid/testdata/vectors.json — the
// others being the Go package of the same name and the release CLI. It exists
// because the browser needs to answer the same questions the server does: what
// version is this, is this string a version at all, and is this release a
// testing candidate or a released one. A second set of rules here is how the
// two sides come to disagree about a version they were both shown.
//
// THREE IDENTITIES, KEPT APART. A product version ("102.1.0") is not a release
// tag ("release/102.1.0"), and neither is a Go module version. Product versions
// have no v prefix; module versions do, because the Go toolchain requires one.

/** The ceiling on a single segment. The same number in every implementation:
 *  the browser has no 64-bit integer at the top of its range, so a limit that
 *  held in Go but not here would let the two disagree about a version they were
 *  both shown. */
export const MAX_SEGMENT = 2147483647

/** Product tags live under this namespace, so a product tag can never be
 *  mistaken for a Go module version, which also begins with a v. */
export const TAG_PREFIX = 'release/'

export class ReleaseIdError extends Error {
  constructor(message: string) {
    super(message)
    this.name = 'ReleaseIdError'
  }
}

export interface ProductVersion {
  major: number
  minor: number
  patch: number
}

export type Scheme = 'product' | 'legacy'

export interface ReleaseTag {
  /** The exact tag as published. What goes in a URL and a git ref, never
   *  reconstructed from the parsed parts. */
  raw: string
  scheme: Scheme
  /** Set only for the product scheme. */
  product?: ProductVersion
}

export type Channel = 'stable' | 'testing'

/**
 * Parses a product version.
 *
 * One to three segments, each a run of ASCII digits, missing trailing segments
 * padded with zero. A fourth segment is REFUSED rather than truncated: the
 * format has three, and silently dropping a segment accepts an input from a
 * format this build does not understand. Leading zeros, signs, whitespace,
 * non-ASCII digits and scientific notation are refused for the same reason —
 * guessing at a malformed version is how two sides end up agreeing on a version
 * neither was given.
 */
export function parseProductVersion(input: string): ProductVersion {
  if (input === '') throw new ReleaseIdError('releaseid: empty')
  const parts = input.split('.')
  if (parts.length > 3) {
    throw new ReleaseIdError(
      `releaseid: ${JSON.stringify(input)} has ${parts.length} segments, the format has three`,
    )
  }
  const segments = [0, 0, 0]
  parts.forEach((part, i) => {
    segments[i] = parseSegment(part, input)
  })
  return { major: segments[0], minor: segments[1], patch: segments[2] }
}

function parseSegment(part: string, whole: string): number {
  if (part === '') throw new ReleaseIdError(`releaseid: ${JSON.stringify(whole)} has an empty segment`)
  for (const ch of part) {
    if (ch < '0' || ch > '9') {
      throw new ReleaseIdError(`releaseid: ${JSON.stringify(part)} is not an ASCII digit`)
    }
  }
  if (part.length > 1 && part[0] === '0') {
    throw new ReleaseIdError(`releaseid: ${JSON.stringify(part)} has a leading zero`)
  }
  const n = Number(part)
  if (!Number.isSafeInteger(n) || n > MAX_SEGMENT) {
    throw new ReleaseIdError(`releaseid: ${JSON.stringify(part)} exceeds ${MAX_SEGMENT}`)
  }
  return n
}

/** Renders the fixed three-segment form. Every published surface uses this. */
export function formatProductVersion(v: ProductVersion): string {
  return `${v.major}.${v.minor}.${v.patch}`
}

/**
 * Orders two product versions, -1 / 0 / +1.
 *
 * Segment by segment as integers: 102.1.10 is above 102.1.9, which any string
 * comparison gets backwards. Only product versions are comparable here; a legacy
 * tag has no place in this ordering.
 */
export function compareProductVersion(a: ProductVersion, b: ProductVersion): number {
  const pairs: Array<[number, number]> = [
    [a.major, b.major],
    [a.minor, b.minor],
    [a.patch, b.patch],
  ]
  for (const [left, right] of pairs) {
    if (left !== right) return left < right ? -1 : 1
  }
  return 0
}

/**
 * Parses a release tag.
 *
 * "release/MAJOR.MINOR.PATCH" is the current scheme, and the version part must
 * be exactly three segments: a tag is a published identity, so the short forms
 * that are legal as parse input do not get releases of their own.
 *
 * Anything beginning with "v" is a LEGACY tag, returned as such without its
 * version being interpreted. That matters for the v-prefixed numeric form:
 * "v102.1.0" is not a product version someone forgot to strip a letter from, it
 * is a legacy identity, and treating it as the former would grant a release
 * credit it has not earned.
 */
export function parseReleaseTag(raw: string): ReleaseTag {
  if (raw.startsWith(TAG_PREFIX)) {
    const body = raw.slice(TAG_PREFIX.length)
    if (body.startsWith('v')) {
      throw new ReleaseIdError(`releaseid: ${JSON.stringify(raw)} puts a v inside the product tag namespace`)
    }
    if ((body.match(/\./g) ?? []).length !== 2) {
      throw new ReleaseIdError(`releaseid: ${JSON.stringify(raw)} is a product tag, which is always three segments`)
    }
    const product = parseProductVersion(body)
    if (product.major === 0) {
      throw new ReleaseIdError(`releaseid: ${JSON.stringify(raw)} has a zero release line, which is not a released identity`)
    }
    return { raw, scheme: 'product', product }
  }
  if (raw.startsWith('v')) {
    // "Keep the old tag as it is" means do not REINTERPRET it — not accept
    // anything starting with a v. A tag is an identity, and recognising a
    // string that merely begins with v would put a value into the support
    // matrix that no release ever published.
    if (!/^v\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$/.test(raw)) {
      throw new ReleaseIdError(`releaseid: ${JSON.stringify(raw)} begins with v but is not a version`)
    }
    return { raw, scheme: 'legacy' }
  }
  throw new ReleaseIdError(`releaseid: ${JSON.stringify(raw)} is neither ${TAG_PREFIX}MAJOR.MINOR.PATCH nor a legacy v-tag`)
}

/**
 * Reads the channel from GitHub's release metadata.
 *
 * From the METADATA, never from the tag text: whether a tag contains a hyphen
 * says nothing about whether its release is a testing candidate, and a product
 * version has no hyphen at all. Returns null for a draft — a draft is not
 * published and has no channel, and defaulting it to stable is how an unreleased
 * build becomes an upgrade target.
 */
export function resolveChannel(draft: boolean, prerelease: boolean): Channel | null {
  if (draft) return null
  return prerelease ? 'testing' : 'stable'
}

/**
 * Orders two historical v-prefixed tags, -1 / 0 / +1.
 *
 * A SEPARATE RULE from compareProductVersion, not the same rule with a flag. The
 * historical order is the project's release order, where v0.0.1-beta11 is above
 * v0.0.1-beta9 — the dotless prerelease form this project actually published,
 * which string comparison gets backwards.
 */
export function compareLegacyTag(a: string, b: string): number {
  const [leftCore, leftPre = ''] = stripPrefix(a).split('-', 2)
  const [rightCore, rightPre = ''] = stripPrefix(b).split('-', 2)
  const leftSegments = leftCore.split('.')
  const rightSegments = rightCore.split('.')
  for (let i = 0; i < leftSegments.length && i < rightSegments.length; i++) {
    const c = compareNumericStrings(leftSegments[i], rightSegments[i])
    if (c !== 0) return c
  }
  if (leftSegments.length !== rightSegments.length) {
    return sign(leftSegments.length - rightSegments.length)
  }
  if (leftPre === rightPre) return 0
  if (leftPre === '') return 1 // a release outranks its own prereleases
  if (rightPre === '') return -1

  const leftIds = leftPre.split('.')
  const rightIds = rightPre.split('.')
  for (let i = 0; i < leftIds.length && i < rightIds.length; i++) {
    const leftNumeric = isAllDigits(leftIds[i])
    const rightNumeric = isAllDigits(rightIds[i])
    if (leftNumeric && rightNumeric) {
      const c = compareNumericStrings(leftIds[i], rightIds[i])
      if (c !== 0) return c
      continue
    }
    if (leftNumeric !== rightNumeric) {
      // A numeric identifier ranks below an alphanumeric one.
      return leftNumeric ? -1 : 1
    }
    const c = comparePrereleaseIdentifier(leftIds[i], rightIds[i])
    if (c !== 0) return c
  }
  return sign(leftIds.length - rightIds.length)
}

function stripPrefix(tag: string): string {
  return tag.startsWith('v') ? tag.slice(1) : tag
}

/** The shared alphabetic prefix first, then the number after it as a number. */
function comparePrereleaseIdentifier(a: string, b: string): number {
  const { prefix: aPrefix, digits: aDigits } = splitTrailingDigits(a)
  const { prefix: bPrefix, digits: bDigits } = splitTrailingDigits(b)
  if (aPrefix !== bPrefix) return aPrefix < bPrefix ? -1 : 1
  if (aDigits === '' || bDigits === '') {
    // One has a numeric suffix the other lacks; whole-identifier comparison
    // keeps "alpha" below "alpha1".
    return a < b ? -1 : a > b ? 1 : 0
  }
  return compareNumericStrings(aDigits, bDigits)
}

function splitTrailingDigits(s: string): { prefix: string; digits: string } {
  let i = s.length
  while (i > 0 && s[i - 1] >= '0' && s[i - 1] <= '9') i--
  return { prefix: s.slice(0, i), digits: s.slice(i) }
}

function isAllDigits(s: string): boolean {
  return s !== '' && /^[0-9]+$/.test(s)
}

/** Length first, so a number too large for a double still compares correctly. */
function compareNumericStrings(a: string, b: string): number {
  if (a.length !== b.length) return sign(a.length - b.length)
  return a < b ? -1 : a > b ? 1 : 0
}

function sign(n: number): number {
  return n < 0 ? -1 : n > 0 ? 1 : 0
}

/**
 * The historical VERSION rule, which is stricter than the historical TAG rule
 * above, and deliberately so.
 *
 * The tag rule CLASSIFIES a published name and is permissive: a tag it wrongly
 * rejects is a release that can no longer be read. A version rule VALIDATES a
 * string a caller is about to act on, so it refuses what only looks close —
 * leading zeroes, a missing segment, build metadata, and a redundant leading
 * zero in a numeric prerelease identifier.
 */
const LEGACY_VERSION_SHAPE = /^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$/

function validLegacyVersion(value: string): boolean {
  if (!LEGACY_VERSION_SHAPE.test(value)) return false
  const dash = value.indexOf('-')
  if (dash < 0) return true
  return value
    .slice(dash + 1)
    .split('.')
    .every(segment => !(segment.length > 1 && segment[0] === '0' && /^[0-9]+$/.test(segment)))
}

/**
 * The version a release is identified by, in either scheme — the string a daemon
 * is stamped with and the string the catalog carries. Undefined for anything
 * that is not a version in either scheme.
 *
 * A TAG IS NOT A VERSION and this returns undefined for one: `release/4.0.0` is
 * an address, `4.0.0` is the thing. A legacy version has no namespace of its
 * own, so it IS its own tag; a product version has one, which is what
 * tagForVersion adds.
 */
export function canonicalReleaseVersion(input: string): string | undefined {
  if (typeof input !== 'string' || input === '' || input.includes('+')) return undefined
  if (input.startsWith('v')) {
    return validLegacyVersion(input) ? input : undefined
  }
  // Three segments, always: a version is a published identity, and the short
  // forms parseProductVersion pads are input convenience, not identities.
  if ((input.match(/\./g) ?? []).length !== 2) return undefined
  try {
    const product = parseProductVersion(input)
    if (product.major === 0) return undefined
    return formatProductVersion(product)
  } catch {
    return undefined
  }
}

/**
 * The tag a version is published under.
 *
 * NOT ALWAYS THE VERSION. A legacy release is published under its version
 * unchanged; a product release is published under `release/` + its version,
 * because the namespace is what keeps a product tag from being mistaken for a Go
 * module version — it is part of the ADDRESS, not part of the version. A caller
 * that puts a version where a tag belongs asks for a release that does not
 * exist, and the 404 reads as "no such release" rather than "wrong identity".
 */
export function tagForVersion(version: string): string | undefined {
  const canonical = canonicalReleaseVersion(version)
  if (canonical === undefined) return undefined
  return canonical.startsWith('v') ? canonical : TAG_PREFIX + canonical
}

/**
 * Whether the string is a tag one of this project's releases could be published
 * under.
 *
 * One implementation with parseReleaseTag, deliberately: a second shape check
 * beside the parser is the arrangement that lets a value be accepted here and
 * refused there.
 */
export function isReleaseTag(tag: string): boolean {
  try {
    parseReleaseTag(tag)
    return true
  } catch {
    return false
  }
}

/**
 * The tag a release is ADDRESSED by, from the two strings a catalog entry
 * carries: the version, and the tag the PANEL stated if it sent one.
 *
 * THE STATED VALUE WINS, because the panel knows the tag it published while this
 * only re-derives it — and the derivation stays for a panel older than the field,
 * pinned by the shared vectors so it cannot drift from the rule the panel
 * applies.
 *
 * A STATED TAG IS STILL CHECKED. Taking "the panel said so" as the same thing as
 * "it is one of our tags" would leave this value — which goes into an href —
 * validated by nothing at all; a tag that fails the rule is refused rather than
 * quietly re-derived, because falling back would address a release the panel did
 * not name.
 *
 * It lives here, and takes two strings, so that a pure module does not have to
 * import the API layer to answer the question — which it did, and which dragged
 * an HTTP client into a node-environment test.
 */
export function releaseTag(version: string, stated?: string): string | undefined {
  if (stated !== undefined) {
    return isReleaseTag(stated) ? stated : undefined
  }
  return tagForVersion(version)
}
