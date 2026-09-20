import { describe, expect, it } from 'vitest'
import vectors from './productVersion.vectors.json'
import {
  MAX_SEGMENT,
  TAG_PREFIX,
  canonicalReleaseVersion,
  compareProductVersion,
  compareReleaseVersion,
  formatProductVersion,
  parseProductVersion,
  parseReleaseTag,
  isReleaseTag,
  resolveChannel,
  releaseTag,
  tagForVersion,
} from './productVersion'

// The vectors are the contract, shared with the Go package of the same name and
// the release CLI. This test reads the file rather than carrying its own table,
// for the same reason the Go one does: two tables drift, and the drift is
// invisible until a release is refused by one side and accepted by the other.

describe('product version vectors', () => {
  it('agrees with the vectors about the segment ceiling', () => {
    expect(vectors.max_segment).toBe(MAX_SEGMENT)
  })

  it('normalises short forms to three segments', () => {
    for (const tc of vectors.normalize) {
      expect(formatProductVersion(parseProductVersion(tc.in)), `${tc.in} -> ${tc.out}`).toBe(tc.out)
    }
  })

  it('refuses invalid input rather than repairing it', () => {
    for (const tc of vectors.reject) {
      expect(() => parseProductVersion(tc.in), `${tc.in} (${tc.why})`).toThrow()
    }
  })

  it('orders segment by segment, numerically', () => {
    for (const tc of vectors.order) {
      const a = parseProductVersion(tc.a)
      const b = parseProductVersion(tc.b)
      expect(compareProductVersion(a, b), `${tc.a} vs ${tc.b}`).toBe(tc.cmp)
      // `-0` is not `0` under Object.is, so negate explicitly rather than
      // letting the arithmetic produce a signed zero.
      const reversed = tc.cmp === 0 ? 0 : -tc.cmp
      expect(compareProductVersion(b, a), `${tc.b} vs ${tc.a} (antisymmetry)`).toBe(reversed)
    }
  })

  it('carries the scheme of each tag', () => {
    for (const tc of vectors.tags) {
      const tag = parseReleaseTag(tc.in)
      expect(tag.scheme, tc.in).toBe(tc.scheme)
      if (tc.version) {
        expect(formatProductVersion(tag.product!), tc.in).toBe(tc.version)
      }
    }
  })

  // isReleaseTag is the predicate the catalog's stated tag goes through, so it is
  // held to the same two sections as the parser it delegates to.
  it('agrees with the vectors about which strings are tags', () => {
    for (const tc of vectors.tags) expect(isReleaseTag(tc.in), tc.in).toBe(true)
    for (const tc of vectors.reject_tags) expect(isReleaseTag(tc.in), `${tc.in} (${tc.why})`).toBe(false)
  })

  // releaseTag reads the two strings a catalog entry carries. The vectors supply
  // the tags, so the stated-value half is checked against the same data the
  // derivation is.
  it('prefers the stated tag and falls back to the derived one', () => {
    const [first] = vectors.tags
    expect(releaseTag('4.0.0', first.in)).toBe(first.in)
    // A stated tag that is not one of ours is refused rather than re-derived:
    // falling back would address a release the panel did not name.
    expect(releaseTag('4.0.0', 'not-a-tag')).toBeUndefined()
    for (const tc of vectors.reject_tags) expect(releaseTag('4.0.0', tc.in), tc.in).toBeUndefined()
    // Absent, the derivation answers — which is every panel older than the field.
    expect(releaseTag('4.0.0')).toBe(`${TAG_PREFIX}4.0.0`)
  })

  it('refuses tags that are not tags', () => {
    for (const tc of vectors.reject_tags) {
      expect(() => parseReleaseTag(tc.in), `${tc.in} (${tc.why})`).toThrow()
    }
  })

  it('reads the channel from release metadata, never from the tag text', () => {
    for (const tc of vectors.channels) {
      const got = resolveChannel(tc.draft, tc.prerelease)
      if (tc.channel === '') {
        expect(got, `draft=${tc.draft} prerelease=${tc.prerelease}`).toBeNull()
      } else {
        expect(got, `draft=${tc.draft} prerelease=${tc.prerelease}`).toBe(tc.channel)
      }
    }
  })

  it('accepts and refuses versions the way the vectors say', () => {
    // The version shape, which the Go side checks against the same section, so
    // the two readings are held to one piece of data instead of to each other.
    for (const tc of vectors.versions as Array<{ in: string; scheme?: string; ok: boolean; why?: string }>) {
      const canonical = canonicalReleaseVersion(tc.in)
      if (!tc.ok) {
        expect(canonical, `${tc.in} (${tc.why ?? ''})`).toBeUndefined()
        continue
      }
      expect(canonical, `${tc.in} (${tc.why ?? ''})`).toBe(tc.in)
      const tag = tagForVersion(tc.in)
      expect(tag, tc.in).toBe(`${TAG_PREFIX}${tc.in}`)
    }
  })

  it('does not read a tag as a version, or a version as a tag', () => {
    for (const tc of vectors.tags) {
      // A tag is an address. The version inside it is versionOfTag's business,
      // and asking the version rule about the tag must not answer yes.
      if (tc.scheme === 'product') {
        expect(canonicalReleaseVersion(tc.in), tc.in).toBeUndefined()
        expect(tagForVersion(tc.in), tc.in).toBeUndefined()
      }
    }
  })

  // THE STRING COMPARATOR IS THE ONE THE UPGRADE LIST USES, and it is the same
  // rule as the typed one: the catalog carries versions as strings, so a second
  // implementation here would be a second opinion about whether an upgrade is an
  // upgrade.
  it('orders version strings the way it orders parsed ones', () => {
    // THE VERSIONS SECTION, NOT THE ORDER SECTION. The order section walks the
    // comparator over inputs that are orderable but are not identities — `0.0.0`
    // is one — and this function refuses to order a string it cannot first
    // identify. Walking all PAIRS of identified versions is the stronger check
    // anyway: it covers the fourth segment and the ties as well.
    const identified = (vectors.versions as Array<{ in: string; ok: boolean }>)
      .filter(tc => tc.ok)
      .map(tc => tc.in)
    for (const a of identified) {
      for (const b of identified) {
        const want = compareProductVersion(parseProductVersion(a), parseProductVersion(b))
        expect(compareReleaseVersion(a, b), `${a} vs ${b}`).toBe(want)
      }
    }
    // An unparseable input compares EQUAL, which the callers turn into a
    // refusal: a version that cannot be shown to be newer is not newer.
    expect(compareReleaseVersion('latest', '4.0.0')).toBe(0)
    expect(compareReleaseVersion('4.0.0', 'not-a-version')).toBe(0)
    // A TAG IS NOT A VERSION, so it is not orderable here either.
    expect(compareReleaseVersion('release/4.0.0', '4.0.0')).toBe(0)
  })
})

describe('a legacy tag is not one of ours', () => {
  it('does not turn a legacy tag into a product version, or accept it as a tag', () => {
    // The tempting bug: strip the v and treat it as a product version. It would
    // grant a release an identity this project no longer publishes — and the
    // other tempting bug is to keep answering for the old scheme, which is how a
    // string nobody can install keeps being offered.
    expect(() => parseProductVersion('v102.1.0')).toThrow()
    expect(() => parseReleaseTag('v102.1.0')).toThrow()
    expect(isReleaseTag('v102.1.0')).toBe(false)
    expect(canonicalReleaseVersion('v102.1.0')).toBeUndefined()
    expect(tagForVersion('v102.1.0')).toBeUndefined()
  })

  // A FOURTH SEGMENT IS A VERSION NOW. This asserted the opposite until the
  // format gained the optional BUILD component; what has not changed is that
  // nothing is ever TRUNCATED, so the fifth is still refused.
  it('does not truncate a fifth segment', () => {
    expect(() => parseProductVersion('102.1.0.1.2')).toThrow()
    expect(() => parseReleaseTag('release/102.1.0.1.2')).toThrow()
    expect(parseProductVersion('102.1.0.1').build).toBe(1)
    expect(parseReleaseTag('release/102.1.0.1').product?.build).toBe(1)
  })

  it('does not give the short form a release of its own', () => {
    expect(() => parseReleaseTag('release/102.1')).toThrow()
    expect(() => parseReleaseTag('release/102')).toThrow()
  })
})
