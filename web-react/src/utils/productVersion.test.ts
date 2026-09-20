import { describe, expect, it } from 'vitest'
import vectors from './productVersion.vectors.json'
import {
  MAX_SEGMENT,
  TAG_PREFIX,
  canonicalReleaseVersion,
  compareLegacyTag,
  compareProductVersion,
  formatProductVersion,
  parseProductVersion,
  parseReleaseTag,
  resolveChannel,
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

  it('accepts and refuses versions the way both schemes say', () => {
    // The version shape, which the Go side checks against the same section,
    // so the two readings are held to one piece of data instead of to each
    // other.
    //
    // A `v` PREFIXED TO A PRODUCT VERSION IS NOT AN ERROR AND IS NOT ASSERTED
    // AS ONE: `v1.0.0` is the legacy identity for those same numbers, and the
    // scheme separation is that a product version never carries the prefix,
    // not that the prefix poisons the string.
    for (const tc of vectors.versions as Array<{ in: string; scheme?: string; ok: boolean; why?: string }>) {
      const canonical = canonicalReleaseVersion(tc.in)
      if (!tc.ok) {
        expect(canonical, `${tc.in} (${tc.why ?? ''})`).toBeUndefined()
        continue
      }
      expect(canonical, `${tc.in} (${tc.why ?? ''})`).toBe(tc.in)
      const tag = tagForVersion(tc.in)
      expect(tag, tc.in).toBe(tc.scheme === 'product' ? `${TAG_PREFIX}${tc.in}` : tc.in)
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

  it('keeps the legacy ordering, whose dotless prereleases sort numerically', () => {
    for (const tc of vectors.legacy_order) {
      expect(compareLegacyTag(tc.a, tc.b), `${tc.a} vs ${tc.b}`).toBe(tc.cmp)
    }
  })
})

describe('the two schemes are not interchangeable', () => {
  it('does not turn a legacy tag into a product version', () => {
    // The tempting bug: strip the v and treat it as a product version. It would
    // grant a release an identity it never published.
    expect(() => parseProductVersion('v102.1.0')).toThrow()
    expect(parseReleaseTag('v102.1.0').scheme).toBe('legacy')
  })

  it('does not truncate a fourth segment', () => {
    expect(() => parseProductVersion('102.1.0.1')).toThrow()
    expect(() => parseReleaseTag('release/102.1.0.1')).toThrow()
  })

  it('does not give the short form a release of its own', () => {
    expect(() => parseReleaseTag('release/102.1')).toThrow()
    expect(() => parseReleaseTag('release/102')).toThrow()
  })
})
