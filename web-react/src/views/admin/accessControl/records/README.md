The records parameter layer implements the shared-link grammar for the planned
records tab. It does not yet mount a records page.

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

The records page, browser acceptance and deployment validation remain open.
