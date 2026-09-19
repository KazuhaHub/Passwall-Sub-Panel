/**
 * Options shared by read endpoints.
 *
 * `signal` must reach axios or a query that unmounts (or changes params) keeps
 * its HTTP request alive; `silent` suppresses the global error toast for
 * callers that render their own affordance.
 */
export interface ReadOptions {
  signal?: AbortSignal
  silent?: boolean
}
