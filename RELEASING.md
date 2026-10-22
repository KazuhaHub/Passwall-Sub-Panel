# Release versions and channels

New releases use `MAJOR.MINOR.PATCH`, optionally followed by `-beta.N` or
`-rc.N`. Counters start at 1, have no leading zero, and compare numerically.
Only beta and rc stages are published. Build metadata (`+...`) is not part of a
release identity. A tag is `v` plus the exact version; archives, image tags,
and the binary's version stamp use the version without `v`.
The panel reads the running plain version's release metadata before displaying
Stable or Testing; offline/unverified metadata is shown as unknown. A beta/rc
suffix proves Testing without a network lookup.

- Increment PATCH for compatible fixes, MINOR for compatible features, MAJOR for
  incompatible changes. Panel and Node version independently.
- A new line is named explicitly; the ordinary next feature line is `4.1`.
  Automatic candidate allocation starts that line at `4.1.0-beta.1`.
- Another beta increments N. RC is explicit. A beta cannot follow an RC of the
  same target. Once a target is final, the next automatic fix targets PATCH+1.
- A failed publication may leave a gap. Tags and exact-version assets are never
  moved or overwritten. Retry the original run and identity.
- Historical four-segment versions and `release/` addresses remain readable.
  They keep their original order and URLs. New publication refuses those forms.

## Channels

| Surface | Formal channel | Early-access channel |
| --- | --- | --- |
| Name / API preference | `stable` | `testing` |
| Docker canonical alias | `stable` | `testing` |
| Compatibility alias | `latest` | `beta` |

Testing opts into early candidates and also receives final releases; it is a
broader update preference, not a promise that every selected build has a beta
suffix. The two testing aliases track the newest publication. Stable pointers
move only through a manual verified promotion. Exact versions stay pinned.
A beta/rc suffix is always Testing, even if GitHub metadata is accidentally
labelled stable. Numeric historical releases still use their release metadata.

## Candidate and final identities

`4.1.0-beta.2`, `4.1.0-rc.1`, and `4.1.0` are distinct immutable identities.
Never mark a suffixed release stable, rename its archives, or move its tag.

To finalize a tested candidate, create the plain `v4.1.0` tag at the same source
commit and publish its correctly stamped artifacts. They are new bytes because
of the version stamp, so they must pass the final version's checks. Publication
stages that release as GitHub Pre-release with an "Awaiting promotion" title.
After verification, promote the plain release; promotion reuses those exact
artifacts and changes its title and channel pointers. A partial promotion is
retried with the same tag, never with replacement assets.

## Migration order

1. Publish Node `4.0.2` as the plain migration release, complete acceptance, then
   promote it. This introduces suffix parsing and initializes `stable/testing`
   aliases while retaining `latest/beta`.
2. Publish the updated panel. Existing saved `beta` preferences remain accepted;
   newly selected testing preferences use `testing`.
3. Publish candidates on `4.1`, then final releases with the scheme above.

Old Node helpers, including `4.0.1.11`, cannot parse suffixed remote-upgrade
requests. They can remotely reach the plain migration version first, or be
upgraded using the generated installation script. The panel filters impossible
remote targets and refuses them before queueing a task. Old panels also need
upgrading before they can list suffixed releases.

Protocol module versions remain independent Go module versions. No historical
Git tags, releases, or commits are rewritten during this migration.

## Panel workflows

- Dispatch `Release` from tested `main`, set line `4.1`, stage `beta` or `rc`,
  and optionally target `4.1.0`; leave tag blank for allocation.
- For a plain final tag already created at the tested candidate's commit,
  dispatch `Release` on that tag ref and supply that exact tag. A tag push also
  starts publication. Every path requires a successful Test run on main.
- Dispatch `Promote a release to stable` from main with the plain tag. It checks
  successful publication, downloaded archives/checksums, source-bound build
  attestations and the exact image's provenance before moving pointers.
- Promotion uses the `release-promotion` environment. Configure its reviewers
  according to the repository's release policy.
