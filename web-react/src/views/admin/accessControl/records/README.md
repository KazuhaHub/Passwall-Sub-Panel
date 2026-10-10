The records parameter layer implements the shared-link grammar for the planned
records tab. It does not mount a records page or call an unwired endpoint.

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

The query adapter, filtered/grouped backend reads and the records page remain
separate implementation and acceptance work.
