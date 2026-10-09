# Destination list audit evidence (C2)

This checkpoint records parser, cache, persistence and native-config evidence.
It does not close stage 1c or establish real candidate Node enforcement.

## Fixed upstream data and filtering

The mandatory offline fixture is release `20261004053124`, with 3,614,228
decompressed bytes and SHA-256
`c0f7da9a7f95c86b354002650e8268b9d6bb0b638229274d3afa5651a7cf74b8`.
The original release checksum is verified before parsing. The compiler
acceptance test additionally pins the expected byte count and digest, so
changing both the fixture and its adjacent checksum cannot silently change
the accepted upstream baseline.

| Requirement | Automated evidence |
| --- | --- |
| Finance remains usable; `domain:hsbc` has a broad-entry report with a source ordinal | `TestGeositeVerifiedReleaseFixture`; `TestVerifiedFinanceCategoryCompilesWithoutBroadEntriesForEveryAction` |
| Complete filtered finance content reaches allow, block, observe, trial allowlist and enforced allowlist without broad entries | `TestVerifiedFinanceCategoryCompilesWithoutBroadEntriesForEveryAction` compares every non-catch-all rule with the complete canonical category |
| Public suffix, short keyword, broad regexp and broad IPv4/IPv6 prefixes are filtered; remote input fails as a whole | `TestGeositeAllBroadClassesAndBoundedUTF8Reports`; `TestRemoteBroadEntriesFailTheWholeRefresh` |
| Attribute intersection, literal `!cn`, both separators, original ordinals and deduplication | `TestGeositeListsCategoriesAndLiteralAttributes`; `TestGeositeFiltersBroadEntriesAndKeepsSourceOrdinals` |
| At most 20 report samples, at most 512 UTF-8 bytes each; full totals survive the cap | `TestGeositeAllBroadClassesAndBoundedUTF8Reports` |
| Empty filtering cannot replace usable content or produce a ready empty category | `TestGeositeEmptyFilterReturnsReportAndNoUsableContent`; `TestListRefreshEmptyCategoryPreservesUsableReportAndEntries` |

## Refresh, cache and publication identity

| Requirement | Automated evidence |
| --- | --- |
| Checksum, YAML and size failures retain the old memory/disk catalog and timestamp | `TestGeositeCacheFailedRefreshPreservesMemoryDiskAndTimestamp` |
| Failed atomic replacement retains memory and removes temporary files | `TestGeositeCacheAtomicReplacementFailurePreservesMemory` |
| Concurrent refresh coalesces; readers remain available while network work is blocked | `TestGeositeCacheRefreshCoalescesAndReadsStayAvailable` |
| One catalog download per due round; hot refresh hours and failed-attempt bounds | `TestListRefreshRoundDownloadsCatalogOnceAndObservesHours`; `TestListFailedRoundsAreBoundedAndChangedSourceRetries` |
| Old successes, errors and reports cannot overwrite an edit/new result or revive a deleted list | `TestListSlowRefreshCannotCommitAfterSourceEditOrDeletion`; `TestDestinationListRefreshCannotOverwriteEditedOrDeletedSources`; `TestDestinationListReportSurvivesSaveReadAndMetadataOnlyRefresh` |
| Report-only refresh preserves generation, published generation, full native config bytes/version/ETag and minted candidate | `TestBuildDestinationReportOnlyRefreshKeepsNativeConfigIdentity` |
| Fresh SQL pool, repositories, list service and disk cache expose the last successful detail/overview report and preview the same content offline | `TestBuildDestinationGeositeReportSurvivesDatabaseAndCacheReopen` |

The two application tests exercise the actual durable refresh commit boundary.
They do not simulate a live upstream download or count restarts in a Node process.
The restart test deliberately gives the persisted successful report different
metadata from the cached catalog, proving that reopening does not reconstruct
and overwrite that report from the cache.

## Validation and remaining gates

On Go 1.27.2, the existing complete destlist, destpolicy, SQL-store, application,
domain and safehttp suites passed before the added checks. The new fixed-finance
compiler check (all five uses), report-only native-config check and application
reopen check passed locally. Windows Application Control blocked local execution
of the new destlist test binary; that check subsequently passed in Linux CI.
No security setting was changed and the blocked binary was not relocated or
executed through an alternative path.

The repository's complete SQLite/race suite runs the mandatory offline fixture;
PostgreSQL and MySQL repeat repository persistence/CAS tests. Each commit needs
its own CI result. Head `43ef19417d4d89e5981538b0a2f9d3a6f0d46f3a` passed its
[complete Test workflow](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37896690523)
and [released-node systemd acceptance](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37896690520),
including all three Linux full-race shards, SQLite full/race, PostgreSQL,
MySQL, static checks, frontend and release-target cross-compilation. Third-party
live-panel adapters were skipped. These are additional acceptance checks of
existing behavior, not claims of newly repaired production defects.

[Native browser evidence](access-control-acceptance/README.md#real-backend-catalog-acceptance)
already records a real catalog download, finance save and report after restart.
Real candidate Node enforcement/restart counts, complete C2 acceptance, C5/C6,
the remaining per-view matrix, owner acceptance and formal release gates remain
open. Released-node systemd success does not substitute for those gates.
