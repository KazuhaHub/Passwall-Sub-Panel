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
  /** The optional fourth segment. ZERO MEANS ABSENT, which is why a literal
   *  trailing zero is refused below: with it accepted, `1.2.3` and `1.2.3.0`
   *  would be two spellings of one version, and a release identity is one string
   *  naming one release. */
  build: number
}

export type Scheme = 'product'

export interface ReleaseTag {
  /** The exact tag as published. What goes in a URL and a git ref, never
   *  reconstructed from the parsed parts. */
  raw: string
  scheme: Scheme
  /** The parsed version, present for every tag this parser accepts. */
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
  if (parts.length > 4) {
    throw new ReleaseIdError(
      `releaseid: ${JSON.stringify(input)} has ${parts.length} segments, the format has at most four`,
    )
  }
  const segments = [0, 0, 0, 0]
  parts.forEach((part, i) => {
    segments[i] = parseSegment(part, input)
  })
  if (parts.length === 4 && segments[3] === 0) {
    throw new ReleaseIdError(
      `releaseid: ${JSON.stringify(input)} has a zero fourth segment, which is another way to write ${segments[0]}.${segments[1]}.${segments[2]}`,
    )
  }
  return { major: segments[0], minor: segments[1], patch: segments[2], build: segments[3] }
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

/** Renders the canonical form: three segments, or four when the BUILD component
 *  is present. Lossless, because a zero fourth is refused at parse time. */
export function formatProductVersion(v: ProductVersion): string {
  if (v.build > 0) return `${v.major}.${v.minor}.${v.patch}.${v.build}`
  return `${v.major}.${v.minor}.${v.patch}`
}

/**
 * Orders two product versions, -1 / 0 / +1.
 *
 * Segment by segment as integers: 102.1.10 is above 102.1.9, which any string
 * comparison gets backwards.
 *
 * THE BUILD SEGMENT IS PART OF THE ORDER, and it was not: comparing the first
 * three segments made `4.0.0` and `4.0.0.1` equal here while the Go side ranked
 * the rebuild above its base. A rebuild could then never be installed, because
 * the node refuses a target that does not compare above its current version.
 */

export function compareProductVersion(a: ProductVersion, b: ProductVersion): number {
  const pairs: Array<[number, number]> = [
    [a.major, b.major],
    [a.minor, b.minor],
    [a.patch, b.patch],
    [a.build, b.build],
  ]
  for (const [left, right] of pairs) {
    if (left !== right) return left < right ? -1 : 1
  }
  return 0
}

/**
 * Parses a release tag.
 *
 * "release/MAJOR.MINOR.PATCH[.BUILD]" is the form, and the version part must be
 * three or four segments: a tag is a published identity, so the short forms that
 * are legal as parse input do not get releases of their own.
 *
 * A v INSIDE THE NAMESPACE (release/v4.0.0) is refused: it would be read as a tag
 * by one rule and a version by another, and the two readings differ about which
 * release it names.
 */
export function parseReleaseTag(raw: string): ReleaseTag {
  if (!raw.startsWith(TAG_PREFIX)) {
    throw new ReleaseIdError(`releaseid: ${JSON.stringify(raw)} is not a ${TAG_PREFIX}MAJOR.MINOR.PATCH tag`)
  }
  const body = raw.slice(TAG_PREFIX.length)
  if (body.startsWith('v')) {
    throw new ReleaseIdError(`releaseid: ${JSON.stringify(raw)} puts a v inside the product tag namespace`)
  }
  const dots = (body.match(/\./g) ?? []).length
  if (dots !== 2 && dots !== 3) {
    throw new ReleaseIdError(`releaseid: ${JSON.stringify(raw)} is a product tag, which is three or four segments`)
  }
  const product = parseProductVersion(body)
  if (product.major === 0) {
    throw new ReleaseIdError(`releaseid: ${JSON.stringify(raw)} has a zero release line, which is not a released identity`)
  }
  return { raw, scheme: 'product', product }
}

/**
 * Orders two release VERSION strings, -1 / 0 / +1.
 *
 * The typed comparator takes parsed versions; this takes what a catalog carries,
 * which is a string. An unparseable input compares EQUAL, and the callers turn
 * that into a refusal: a version that cannot be shown to be newer is not newer.
 */
export function compareReleaseVersion(a: string, b: string): number {
  const left = canonicalReleaseVersion(a)
  const right = canonicalReleaseVersion(b)
  if (left === undefined || right === undefined) return 0
  return compareProductVersion(parseProductVersion(left), parseProductVersion(right))
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
 * The version a release is identified by — the string a daemon is stamped with
 * and the string the catalog carries. Undefined for anything that is not one.
 *
 * A TAG IS NOT A VERSION and this returns undefined for one: `release/4.0.0` is
 * an address, `4.0.0` is the thing.
 */
export function canonicalReleaseVersion(input: string): string | undefined {
  if (typeof input !== 'string' || input === '' || input.includes('+')) return undefined
  // Three or four segments, never fewer: a version is a published identity, and
  // the short forms parseProductVersion pads are input convenience, not
  // identities. The fourth is the optional BUILD component.
  const dots = (input.match(/\./g) ?? []).length
  if (dots !== 2 && dots !== 3) return undefined
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
 * NEVER THE VERSION ITSELF. A release is published under `release/` + its
 * version, because the namespace is what keeps a tag from being mistaken for a Go
 * module version — it is part of the ADDRESS, not part of the version. A caller
 * that puts a version where a tag belongs asks for a release that does not
 * exist, and the 404 reads as "no such release" rather than "wrong identity".
 */
export function tagForVersion(version: string): string | undefined {
  const canonical = canonicalReleaseVersion(version)
  if (canonical === undefined) return undefined
  return TAG_PREFIX + canonical
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
