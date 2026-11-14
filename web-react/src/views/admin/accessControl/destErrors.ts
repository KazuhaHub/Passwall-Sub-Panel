import { destErrorOf, isDestUnavailable } from '@/api/accessControl'
import { pushSnack } from '@/components/SnackbarHost'
import { limitKindKey } from '@/utils/accessControl'

// WHAT A REFUSED REQUEST SAYS. The server's 400 and 409 answers carry a `code`
// (the API's contract with the page); this turns one into a sentence from the
// bundle, so no raw code and no English server text reaches the admin. A code
// the page does not know reads as the generic sentence. A screen that has a
// better place for a code (a field, an Alert in its own dialog) reads
// destErrorOf itself and falls back to this for the rest.

type T = (key: string, opts?: Record<string, unknown>) => string

const E = 'admin:access_control.errors.'

/** Every code the API can answer, in the bundle under errors.<code>. */
export const DEST_ERROR_CODES = [
  'dest_unavailable', 'dest_bad_request', 'dest_bad_id', 'dest_list_parse_failed', 'dest_list_too_large',
  'dest_list_too_broad', 'dest_list_insecure_url', 'dest_list_in_use', 'dest_list_stale', 'dest_policy_invalid',
  'dest_policy_no_match', 'dest_policy_over_limit', 'dest_policy_state_unavailable', 'dest_policy_stale',
  'dest_policy_order_stale', 'dest_name_taken', 'dest_geosite_busy', 'dest_geosite_update_rejected',
  'dest_geosite_unavailable', 'dest_geoip_unavailable', 'dest_source_rule_not_native', 'dest_source_rule_stale', 'dest_source_rule_invalid',
  'dest_exemption_exists', 'dest_exemption_invalid', 'dest_exception_invalid', 'dest_test_invalid',
  'dest_settings_invalid', 'dest_template_unavailable', 'dest_template_exists', 'dest_hits_range', 'dest_hits_invalid',
] as const

export function destErrorText(t: T, err: unknown): string {
  if (isDestUnavailable(err)) return t(`${E}dest_unavailable`)
  const e = destErrorOf(err)
  if (!e) return t(`${E}generic`)
  if (!e.code) return t(`${E}generic`)
  if ((DEST_ERROR_CODES as readonly string[]).includes(e.code)) {
    const b = e.body
    const kind = typeof b.kind === 'string' ? b.kind : (b.publish_error as { kind?: string } | undefined)?.kind
    return t(`${E}${e.code}`, {
      what: t(`admin:access_control.verdict.what.${limitKindKey(kind)}`),
      used: b.used ?? (b.publish_error as { used?: number } | undefined)?.used,
      limit: b.limit ?? (b.publish_error as { limit?: number } | undefined)?.limit,
    })
  }
  return t(`${E}generic`)
}

/** A snack for a write the page has no better place to explain. */
export function reportDestError(t: T, err: unknown) {
  pushSnack(destErrorText(t, err), 'error')
}

/** A retry's answer: `retried:false` means there was nothing refused to retry, which is not a success. */
export function reportRetry(t: T, res: { retried: boolean }) {
  pushSnack(t(`admin:access_control.coverage.${res.retried ? 'retried' : 'not_retried'}`), res.retried ? 'success' : 'info')
}
