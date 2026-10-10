The records tab mounts from `AccessControlView` only while active. It reads the
administrator hit API and keeps its private cache scoped to the current session.

`parseRecordsParams` reads typed filters without changing the supplied URL.
`recordsSearch` changes only those filters, preserves other tabs and drawer
state, omits defaults and resets the page after a filter change. It strips stale
`q` and `rec_q` parameters and cannot serialize a search value. The records view
must keep its keyword in component state and reset pagination when that changes.

`recordsRequest` resolves relative ranges at request time and converts custom
datetime-local text in browser time to Unix milliseconds. It rejects reversed
or over-31-day ranges; callers must show a validation state instead of querying
with a fallback range. Shared relative day ranges are also bounded by the
effective hit retention. A `deny` selection becomes `action=block` and
`source_kind=group`; block and observe select policy sources.

`api/destinationHits.ts` calls the administrator-only `/admin/dest/hits` API.
The API reads filtered details or site/user/policy aggregates and preserves
nullable deleted-source labels, independent summaries and separate panel-level
loss units. Cancellation and failures propagate to the caller. Its explicit
parameter allowlist excludes page-link/drawer fields accidentally attached at
runtime. The assembled app tests exercise the route and its authorization;
storage tests cover SQLite locally and real server dialects in CI.

`RecordsTab`, `HitsMetricCards` and `HitRow` connect filtering, stable grouped
pages, nullable source/account labels, panel-zone hour/date display, independent
summary cards and separate incomplete loss units. Search is debounced in local
state. Partial custom-time edits remain local and invalid instead of issuing a
fallback query. Relative windows resolve on an actual read, not each render.
Inactive tabs do not read hits, and pending reads cancel on departure.

Row actions reuse the existing destination test prefill, transactional global
exception/account exemption dialogs and in-page account drawer. The retention
footer opens settings with hit retention focused. Stage-5 deny cards and trial
checkboxes stay hidden until group-mode integration. Mobile rows and filter
dialogs have separate responsive layouts; browser visual acceptance remains open.
The HTTP adapter validates its response envelope, including the requested
group/page, separate loss units and incomplete flag. SPA fallback HTML or corrupt
histories become an unavailable read, never an empty or complete result.

Browser acceptance and real deployment validation remain open.
