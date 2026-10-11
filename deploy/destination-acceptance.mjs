import { readFileSync } from 'node:fs'
import { pathToFileURL } from 'node:url'

export const nodeCandidate = Object.freeze({ repository: 'KazuhaHub/Passwall-Node', commit: '63c60f6d5e8cd093bbaa8385f5b514dbe2f40390' })
export const guardedNodeCandidate = Object.freeze({ repository: nodeCandidate.repository, commit: 'a6163c8b5c10a9c8129cf660020fe2c8fa2b85db' })
export const destinationCases = Object.freeze(['empty', 'watch', 'block', 'paused', 'trial', 'enforce', 'extra', 'idle_sync', 'report_refresh', 'listener_rejected', 'core_check_timeout', 'collection_off', 'usage', 'restart', 'policy_rejected', 'fallback_port', 'fallback_pause', 'fallback_prune', 'sniff_exhaustion', 'exhausted_roster', 'exhausted_panel_restart', 'fallback_group_open', 'fallback_group_pause', 'offline_pause', 'offline_restart', 'response_loss'])
const pkg = 'github.com/KazuhaHub/passwall-sub-panel/internal/app'
export const singBoxCases = Object.freeze(['empty', 'watch', 'block', 'paused', 'domain', 'trial', 'enforce', 'extra', 'idle_sync', 'report_refresh', 'restart', 'collection_off', 'private_baseline'])
export const guardedSingBoxCases = Object.freeze([...singBoxCases.filter(name => name !== 'private_baseline'), 'private_guard'])
const xrayNames = ['TestNativeDestinationTraffic', ...destinationCases.map(name => `TestNativeDestinationTraffic/${name}`)]
const singBoxNames = ['TestNativeSingBoxDestinationTraffic', ...singBoxCases.map(name => `TestNativeSingBoxDestinationTraffic/${name}`)]
const guardedSingBoxNames = ['TestNativeGuardedSingBoxDestinationTraffic', ...guardedSingBoxCases.map(name => `TestNativeGuardedSingBoxDestinationTraffic/${name}`)]
const names = [...xrayNames, ...singBoxNames, ...guardedSingBoxNames]
const diagnostics = Object.freeze({
  'timed out waiting for real policy rejection and LKG receipt': 'policy_rejection_timeout',
  'real rejection did not identify the canonical desired policy': 'policy_rejection_identity_failed',
  'status API lost the confirmed rejected-policy fallback': 'policy_rejection_status_failed',
  'policy rejection changed the original live core artifact': 'policy_rejection_artifact_changed',
  'policy retry did not reset the real rejection': 'policy_rejection_retry_failed',
  'timed out waiting for rejected policy LKG watch ingestion': 'policy_rejection_watch_timeout',
  'report refresh requires the immutable owned source fixture': 'refresh_source_invalid',
  'report-only remote refresh failed': 'refresh_fetch_failed',
  'timed out waiting for report-only remote refresh': 'refresh_commit_timeout',
  'report-only refresh was not readable through administrator HTTP': 'refresh_read_failed',
  'idle sync cannot bind a unique owned live core': 'idle_core_binding_failed',
  'idle sync replaced or lost the original core process': 'idle_core_restarted',
  'idle sync has no confirmed core config': 'idle_config_missing',
  'idle sync did not accept the unchanged applied policy': 'idle_policy_changed',
  'idle sync changed the confirmed core config': 'idle_config_changed',
  'timed out waiting for unchanged accepted idle sync': 'idle_sync_timeout',
  'cannot bind timeout fault to the owned running core': 'timeout_core_binding_failed',
  'did not pause the owned actual Xray config check': 'timeout_check_not_paused',
  'timed out waiting for real core check timeout': 'timeout_report_wait_failed',
  'timed out waiting for timeout repair application': 'timeout_repair_failed',
  'core timeout was presented as a policy rejection': 'timeout_status_failed',
  'real VLESS traffic did not reach its nonce target': 'traffic_allow_failed',
  'real VLESS traffic bypassed a deny rule': 'traffic_deny_failed',
  'deny/allow result disagreed with actual target request count': 'traffic_count_mismatch',
  'timed out waiting for current policy receipt': 'policy_receipt_timeout',
  'timed out waiting for real hit ingestion': 'hit_ingestion_timeout',
  'timed out waiting for real usage ingestion': 'usage_ingestion_timeout',
  'private access data escaped into Node output': 'output_privacy_failed',
  'administrator route': 'admin_http_status_failed',
  'empty policy collected private history': 'empty_collected_data',
  'real trial report lost anonymity': 'trial_anonymity_failed',
  'off admitted new audit data or erased history': 'collection_off_failed',
  'usage HTTP did not expose the real bounded observation': 'usage_read_failed',
  'real private usage read did not leave an audit record': 'usage_audit_failed',
  'first restarted report did not restore the confirmed policy': 'restart_receipt_failed',
  'timed out waiting for first restarted report': 'restart_report_timeout',
  'timed out waiting for restarted user hit ingestion': 'restart_hit_timeout',
  'timed out waiting for exhausted roster application': 'roster_revoke_timeout',
  'fault candidate did not retain its exact rules and collection mode': 'fault_candidate_failed',
  'fault candidate changed its canonical body or historical LKG': 'fault_lkg_failed',
  'status API did not expose the exact native node': 'status_node_failed',
  'status API disagreed with the currently acknowledged candidate': 'status_candidate_failed',
  'port fallback contains unexpected rules': 'fallback_rule_count_failed',
  'fallback did not execute the confirmed port-only rule': 'fallback_rule_failed',
  'roster update rearmed an exhausted fallback': 'roster_rearmed_fallback',
  'timed out waiting for restored PSP sync': 'panel_restart_sync_timeout',
  'PSP reopen rearmed an exhausted fallback or lost its history': 'panel_restart_rearmed_fallback',
  'timed out waiting for real listener rejection': 'listener_rejection_timeout',
  'timed out waiting for listener repair application': 'listener_repair_timeout',
  'listener failure fixture has no confirmed config': 'listener_baseline_failed',
  'listener failure changed allowlist client membership': 'listener_membership_changed',
  'listener failure retired an allowlist member': 'listener_member_retired',
  'listener failure replaced an allowlist client': 'listener_member_replaced',
  'listener failure was presented as a policy fault': 'listener_policy_fault',
  'real listener rejection did not reach the administrator inbox': 'listener_inbox_failed',
  'group fallback retained an unexpected deny rule': 'group_deny_failed',
  'group fallback did not retain the exact watch rule': 'group_watch_failed',
  'group fallback contains an unexpected source': 'group_source_failed',
  'group fallback has the wrong source counts': 'group_source_count_failed',
  'timed out waiting for open fallback roster application': 'group_open_roster_timeout',
  'paused group fallback collected a new watch hit': 'group_pause_collected',
  'timed out waiting for resumed group fallback hit ingestion': 'group_resume_hit_timeout',
  'timed out waiting for offline pause status': 'offline_pause_timeout',
  'offline node was recorded as having stopped execution': 'offline_pause_receipt_failed',
  'timed out waiting for offline baseline hit ingestion': 'offline_baseline_timeout',
  'timed out waiting for offline frozen block batch': 'offline_batch_timeout',
  'timed out waiting for offline immutable batch replay': 'offline_replay_timeout',
  'offline retry changed a frozen batch or reached storage': 'offline_frozen_batch_failed',
  'timed out waiting for offline restarted reports': 'offline_restart_report_timeout',
  'offline restart did not restore its applied policy before sync': 'offline_restart_policy_failed',
  'offline restart retained unacknowledged memory backlog': 'offline_restart_backlog_failed',
  'offline restart replaced or exposed its sink credential': 'offline_restart_token_failed',
  'timed out waiting for new offline restart hit': 'offline_new_hit_timeout',
  'timed out waiting for reconnected new hit ingestion': 'offline_reconnect_hit_timeout',
  'reconnection replayed pre-restart memory backlog': 'offline_replayed_old_backlog',
  'timed out waiting for fresh recovered hit ingestion': 'offline_recovered_hit_timeout',
  'timed out waiting for committed response loss batch': 'response_loss_commit_timeout',
  'timed out waiting for committed response loss replay': 'response_loss_replay_timeout',
  'committed response loss changed the frozen batch or duplicated storage': 'response_loss_duplicate_failed',
  'timed out waiting for response loss backlog ingestion': 'response_loss_backlog_timeout',
  'restored response replay duplicated committed hits': 'response_loss_recovery_duplicate',
  'timed out waiting for response loss fresh hit ingestion': 'response_loss_fresh_timeout',
  'timed out waiting for current sing-box policy receipt': 'singbox_receipt_timeout',
  'sing-box execution falsely claimed collection': 'singbox_collection_failed',
  'sing-box persisted destination audit data': 'singbox_storage_failed',
  'sing-box restart lost its applied policy receipt': 'singbox_restart_failed',
  'timed out waiting for first sing-box restart report': 'singbox_restart_timeout',
})
function parseEvent(line) {
  try { return JSON.parse(line) } catch { throw new Error('Malformed test execution stream; content withheld') }
}
export function sanitizeDestinationResults(input) {
  return input.split(/\r?\n/).filter(line => line.trim()).map(line => {
    const event = parseEvent(line)
    if (event.Action === 'output') {
      const category = typeof event.Output === 'string' && /native diagnostic: (download|checksum|timeout|rejected|unsupported|permission|bind|certificate|listener_issue|listener_core_rejected|listener_agent_fresh|listener_policy_ready|listener_policy_unchanged|listener_epoch|listener_config_changed|listener_config_preserved|listener_config_fresh|receipt_generation|receipt_mint_fresh|receipt_sync_fresh|receipt_state|receipt_sha|receipt_lkg|receipt_precheck|receipt_streams|receipt_transition)=(true|false)(?:\r?\n|$)/.exec(event.Output)
      if (category) return JSON.stringify({ Action: 'output', Package: pkg, Diagnostic: category[1], Observed: category[2] === 'true' })
      const sequence = typeof event.Output === 'string' && /native receipt wait: ([1-9][0-9]?)(?:\r?\n|$)/.exec(event.Output)
      if (sequence) return JSON.stringify({ Action: 'output', Package: pkg, Diagnostic: 'receipt_wait', Observed: Number(sequence[1]) })
      const diagnostic = Object.entries(diagnostics).find(([message]) => typeof event.Output === 'string' && event.Output.includes(message))?.[1]
      return diagnostic ? JSON.stringify({ Action: 'output', Package: pkg, ...(names.includes(event.Test) ? { Test: event.Test } : {}), Diagnostic: diagnostic }) : ''
    }
    if (!['start', 'run', 'pause', 'cont', 'pass', 'bench', 'fail', 'skip'].includes(event.Action)) throw new Error('Unrecognized test event; content withheld')
    const result = { Action: event.Action, Package: event.Package === pkg ? pkg : '[other package]' }
    if (names.includes(event.Test)) result.Test = event.Test
    if (typeof event.Elapsed === 'number' && Number.isFinite(event.Elapsed)) result.Elapsed = event.Elapsed
    return JSON.stringify(result)
  }).filter(Boolean).join('\n') + '\n'
}
export function checkDestinationResults(input) { checkCases(input, xrayNames) }
export function checkSingBoxResults(input) { checkCases(input, singBoxNames) }
export function checkGuardedSingBoxResults(input) { checkCases(input, guardedSingBoxNames) }
function checkCases(input, names) {
  const runs = new Map(), passes = new Map()
  let packagePasses = 0
  for (const line of input.split(/\r?\n/).filter(line => line.trim())) {
    const event = parseEvent(line)
    if (event.Action === 'fail' || event.Action === 'skip') throw new Error('Acceptance failed or skipped; private output withheld')
    if (event.Package !== pkg) continue
    if (!event.Test && event.Action === 'pass') packagePasses++
    if (!names.includes(event.Test)) continue
    if (event.Action === 'run') runs.set(event.Test, (runs.get(event.Test) ?? 0) + 1)
    if (event.Action === 'pass') {
      if (runs.get(event.Test) !== 1) throw new Error('Acceptance pass has no unique execution')
      passes.set(event.Test, (passes.get(event.Test) ?? 0) + 1)
    }
  }
  if (packagePasses !== 1 || names.some(name => runs.get(name) !== 1 || passes.get(name) !== 1)) throw new Error('Acceptance did not execute every required case exactly once')
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  if (process.argv[2] === 'sanitize' && process.argv.length === 3) {
    process.stdout.write(sanitizeDestinationResults(readFileSync(0, 'utf8')))
  } else {
    const checks = { check: checkDestinationResults, 'check-singbox': checkSingBoxResults, 'check-guarded-singbox': checkGuardedSingBoxResults }
    if (!Object.hasOwn(checks, process.argv[2]) || process.argv.length !== 4) throw new Error('Usage: destination-acceptance.mjs check|check-singbox|check-guarded-singbox RESULTS')
    const check = checks[process.argv[2]]
    check(readFileSync(process.argv[3], 'utf8'))
    console.log('Every native destination acceptance case ran and passed')
  }
}
