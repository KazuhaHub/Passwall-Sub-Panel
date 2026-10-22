# Release versions and channels

New releases use `MAJOR.MINOR.PATCH`, optionally followed by `-beta.N` or
`-rc.N`. Counters start at 1, have no leading zero, and compare numerically.
Candidate stages are beta and rc. Build metadata (`+...`) is not part of a
release identity. A tag is `v` plus the exact version; archives, image tags,
and the binary's version stamp use the version without `v`.
The panel reads the running plain version's release metadata before displaying
Stable or Pending; offline/unverified metadata is shown as unknown. A beta/rc
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

| Update preference / Docker alias | Accepted stages | Compatibility alias |
| --- | --- | --- |
| `stable` | Approved final releases | `latest` |
| `rc` | RC and approved final releases | — |
| `beta` | Beta, RC and approved final releases | `testing` |

`latest` always follows `stable`. Neither candidate stage can move it. Saved
`testing` preferences remain accepted and open as Beta; new preferences use
`stable`, `rc` or `beta`. Exact versions remain pinned.
A suffix determines Beta or RC even if GitHub metadata is wrong. Plain modern
versions (4.0.2 onward) published as pre-releases are pending approval, and do
not enter any update channel. Historical plain prereleases retain Beta behavior.
Channels choose the highest eligible version, not the most recent publication.
A final promotion advances each eligible image alias only when it is not already
at a newer version: finalizing 4.1.0 cannot reset Beta from 4.2.0-beta.1. Stable
rollback is refused before any promotion write. Registry errors refuse rather
than pretending a pointer is absent.

## Candidate and final identities

`4.1.0-beta.2`, `4.1.0-rc.1`, and `4.1.0` are distinct immutable identities.
Never mark a suffixed release stable, rename its archives, or move its tag.

Dispatch **Advance a published candidate** from main with the published source
tag and target stage `rc` or `stable`. It supports Beta → RC, Beta → final and
RC → final. It validates the source publication and successful main tests, pins
the source tag's commit (even when main has moved), and creates a new immutable
version tag at that commit. A newer sibling candidate refuses a stale source.

The workflow publishes the newly stamped artifacts and waits for publication to
succeed. Node also waits for both architectures' package and container acceptance.
For a final target it then dispatches the existing verified stable promotion,
including the `release-promotion` environment. A plain final is staged as a
pre-release titled "Awaiting promotion" until those checks complete.

Retries resume the same tag and publication rather than incrementing the RC
counter. A conflicting tag is never moved. If publication fails, rerun/fix that
original publication and resume the transition; if acceptance fails, fix/rerun
that acceptance first. A partial stable promotion is retried with the same tag
and verified bytes. Existing candidate assets and release history are preserved.
The manual publish-then-promote route remains available for a plain migration
release or operator recovery. Historical four-segment candidates cannot be
relabelled into this scheme; use the migration bridge below.

## Migration order

1. Publish Node `4.0.2` as the plain migration release, complete acceptance, then
   promote it. This introduces suffix parsing and initializes `stable/rc/beta`
   aliases while retaining `latest/testing`.
2. Publish the updated panel. Existing saved `beta` and `testing` preferences remain
   accepted; new preferences use `stable`, `rc` or `beta`.
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
