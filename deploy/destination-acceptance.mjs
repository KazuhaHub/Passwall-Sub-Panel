import { readFileSync } from 'node:fs'
import { pathToFileURL } from 'node:url'

export const nodeCandidate = Object.freeze({ repository: 'KazuhaHub/Passwall-Node', commit: '63c60f6d5e8cd093bbaa8385f5b514dbe2f40390' })
export const remoteNodeCandidate = Object.freeze({ repository: nodeCandidate.repository, commit: 'c51d145ae0e2db47eb69a640fe8f751318370f59' })
export const guardedNodeCandidate = Object.freeze({ repository: nodeCandidate.repository, commit: 'a6163c8b5c10a9c8129cf660020fe2c8fa2b85db' })
export const integratedNodeCandidate = Object.freeze({ repository: nodeCandidate.repository, commit: 'e2c9dfbfe934d4a61fda84b0a80b4f1cb5dbc719' })
export const moduleNodeCandidate = Object.freeze({ repository: nodeCandidate.repository, commit: '1ce8f26cfbce8d3decf8855dda4349be10778a23' })
export const destinationCases = Object.freeze(['empty', 'watch', 'block', 'group_scope', 'exemption', 'exemption_expired', 'allow_priority', 'paused', 'trial', 'enforce', 'extra', 'idle_sync', 'report_refresh', 'listener_rejected', 'core_check_timeout', 'collection_off', 'usage', 'restart', 'policy_rejected', 'lkg_rejected', 'pruned_rejected', 'fallback_port', 'fallback_pause', 'fallback_prune', 'sniff_exhaustion', 'exhausted_roster', 'exhausted_panel_restart', 'fallback_group_open', 'fallback_group_pause', 'offline_pause', 'offline_restart', 'response_loss'])
const pkg = 'github.com/KazuhaHub/passwall-sub-panel/internal/app'
export const singBoxCases = Object.freeze(['empty', 'watch', 'block', 'group_scope', 'exemption', 'exemption_expired', 'allow_priority', 'paused', 'domain', 'trial', 'enforce', 'extra', 'idle_sync', 'report_refresh', 'restart', 'collection_off', 'private_baseline'])
export const guardedSingBoxCases = Object.freeze([...singBoxCases.filter(name => name !== 'private_baseline'), 'private_guard'])
const xrayNames = ['TestNativeDestinationTraffic', ...destinationCases.map(name => `TestNativeDestinationTraffic/${name}`)]
const singBoxNames = ['TestNativeSingBoxDestinationTraffic', ...singBoxCases.map(name => `TestNativeSingBoxDestinationTraffic/${name}`)]
const guardedSingBoxNames = ['TestNativeGuardedSingBoxDestinationTraffic', ...guardedSingBoxCases.map(name => `TestNativeGuardedSingBoxDestinationTraffic/${name}`)]
export const legacyUpgradeCases = Object.freeze(['old_panel', 'panel_upgrade', 'legacy_publication', 'mixed_fleet', 'mixed_offline_pause', 'node_upgrade', 'node_downgrade', 'node_reupgrade', 'panel_downgrade_offline', 'panel_downgrade', 'panel_reupgrade', 'allowlist_capability_setup', 'allowlist_capability_removed', 'allowlist_capability_restored'])
const legacyUpgradeNames = ['TestNativeLegacyPSPUpgrade', ...legacyUpgradeCases.map(name => `TestNativeLegacyPSPUpgrade/${name}`)]
export const fleetCases = Object.freeze(['native_ready', 'native_publication', 'native_pause', 'native_resume', 'third_party_ready', 'allowlist_trial', 'allowlist_enforce', 'one_peer_offline', 'allowlist_pause', 'allowlist_reopen'])
const fleetNames = ['TestNativeDestinationFleet', ...fleetCases.map(name => `TestNativeDestinationFleet/${name}`)]
export const loadNames = Object.freeze(['TestNativeDestinationWebhookLoad', 'TestNativeDestinationWebhookLoad/single_rule', 'TestNativeDestinationWebhookLoad/fifty_rules'])
export const trialLoadNames = Object.freeze(['TestNativeDestinationTrialLoad'])
export const mixedPressureNames = Object.freeze(['TestNativeDestinationMixedPressure'])
export const idleCostNames = Object.freeze(['TestNativeDestinationIdleCost'])
export const globalBootstrapNames = Object.freeze(['TestNativeDestinationGlobalCohortBootstrap'])
export const globalBatchNames = Object.freeze(['TestNativeDestinationGlobalBatchPressure'])
export const byteFormationNames = Object.freeze(['TestNativeDestinationByteFormation'])
export const globalByteNames = Object.freeze(['TestNativeDestinationGlobalBytePressure'])
export const blockOverflowNames = Object.freeze(['TestNativeDestinationBlockOverflow', 'TestNativeDestinationBlockOverflow/block_overflow'])
export const remoteUpgradeCases = Object.freeze(['candidate_policy', 'health_failure_restore', 'published_rollback', 'restart_recovery', 'idempotent_replay'])
const dockerUpgradeNames = ['TestNativePublishedDockerRollback', ...remoteUpgradeCases.map(name => `TestNativePublishedDockerRollback/${name}`)]
const remoteUpgradeNames = ['TestNativePublishedRemoteRollback', ...remoteUpgradeCases.map(name => `TestNativePublishedRemoteRollback/${name}`)]
const names = [...xrayNames, ...singBoxNames, ...guardedSingBoxNames, ...legacyUpgradeNames, ...remoteUpgradeNames, ...dockerUpgradeNames, ...fleetNames, ...loadNames, ...trialLoadNames, ...blockOverflowNames, ...mixedPressureNames, ...idleCostNames, ...globalBootstrapNames, ...globalBatchNames, ...byteFormationNames, ...globalByteNames]
const diagnostics = Object.freeze({
  'global byte priming traffic failed': 'global_byte_prime_traffic_failed',
  'global byte traffic exceeded original 75-second formation budget': 'global_byte_formation_budget_failed',
  'global byte real formation traffic failed': 'global_byte_formation_traffic_failed',
  'timed out waiting for global byte current policies and paired 200-user rosters': 'global_byte_receipt_timeout',
  'timed out waiting for global byte warmup fully drained': 'global_byte_warmup_timeout',
  'timed out waiting for global byte first class frozen in every actual owner': 'global_byte_prime_timeout',
  'timed out waiting for global byte first packets durable in original SQL': 'global_byte_first_durable_timeout',
  'timed out waiting for global byte original live keys retained behind frozen packets': 'global_byte_live_keys_timeout',
  'global byte carrier fault exceeded original window': 'global_byte_carrier_fault_window_failed',
  'timed out waiting for global byte carrier admission-time exclusive refusals': 'global_byte_carrier_witness_timeout',
  'timed out waiting for global byte fresh owner four original exclusive witnesses': 'global_byte_fresh_witness_timeout',
  'global byte original SQL hold or control path failed': 'global_byte_sql_or_control_failed',
  'timed out waiting for global byte canonical recovery and all independent ledgers drain': 'global_byte_drain_timeout',
  'read original stored formation events': 'byte_formation_stored_query_failed',
  'warm original byte formation route': 'byte_formation_warm_route_failed',
  'verify independent actual control target': 'byte_formation_control_failed',
  'formation padding would fold into an existing trial site': 'byte_formation_domains_invalid',
  'timed out waiting for actual warmup parsed and completely drained': 'byte_formation_warmup_timeout',
  'timed out waiting for real first packets durable while their responses remain suppressed': 'byte_formation_first_durable_timeout',
  'timed out waiting for actual reverse-priority first packet is frozen before priming the next class': 'byte_formation_prime_timeout',
  'original 75-second fault budget cannot form all four live batches': 'byte_formation_fault_budget_failed',
  'real formation traffic failed before response recovery': 'byte_formation_traffic_failed',
  'timed out waiting for real collector retains live keys separately from unacknowledged first packets': 'byte_formation_live_keys_timeout',
  'real response fault exceeded its recorded 75-second window': 'byte_formation_fault_window_failed',
  'timed out waiting for actual canonical retry releases large next packets into original held reservations': 'byte_formation_large_packet_timeout',
  'real byte formation bypassed the original SQL hold': 'byte_formation_sql_hold_failed',
  'timed out waiting for original SQL and real producer fully drain after formation': 'byte_formation_drain_timeout',
  'actual formation traffic does not reconcile with original SQL and losses': 'byte_formation_ledger_failed',
  'formation lost authenticated admission binding or blocked traffic escaped': 'byte_formation_binding_failed',
  ...Object.fromEntries(['state', 'node_samples', 'sample_window', 'access_quiescence', 'frozen_retry', 'ingress_accounting', 'usage_read', 'hit_read', 'budget_read', 'loss_query', 'loss_scan', 'loss_cursor', 'ledger_accounting', 'node_pending', 'psp_pending', 'admission_binding'].map(reason => [`global batch drain: ${reason}\n`, `global_batch_drain_${reason}`])),
  'timed out waiting for global real canonical retry, SQL drain and all owner loss ledgers': 'global_batch_drain_timeout',
  'native setup stage: administrator\n': 'global_setup_stage_administrator',
  'native setup stage: allowed_target\n': 'global_setup_stage_allowed_target',
  'native setup stage: observe_target\n': 'global_setup_stage_observe_target',
  'native setup stage: control_target\n': 'global_setup_stage_control_target',
  'create destination fixture administrator failed; private diagnostics withheld': 'fixture_admin_create_failed',
  'load destination fixture issuer settings failed; private diagnostics withheld': 'fixture_admin_settings_failed',
  'issue destination fixture access token failed; private diagnostics withheld': 'fixture_admin_token_failed',
  'read native fixture automatic port boundary failed; private diagnostics withheld': 'fixture_port_boundary_failed',
  'native fixture has no explicit nonprivileged server range': 'fixture_port_range_invalid',
  'native fixture explicit server range is exhausted': 'fixture_port_range_exhausted',
  'native fixture explicit server range is occupied': 'fixture_port_range_occupied',
  'release owned native fixture port probe failed; private diagnostics withheld': 'fixture_port_probe_release_failed',
  'invalid owned target port': 'fixture_target_port_invalid',
  'create target nonce failed; private diagnostics withheld': 'fixture_target_nonce_failed',
  'read owned target addresses failed; private diagnostics withheld': 'fixture_target_addresses_failed',
  'decode owned addresses failed; private diagnostics withheld': 'fixture_target_addresses_invalid',
  'target address already exists': 'fixture_target_address_conflict',
  'add exact owned target /128 failed; private diagnostics withheld': 'fixture_target_address_add_failed',
  'remove exact owned target /128 failed; private diagnostics withheld': 'fixture_target_address_remove_failed',
  'verify local target route failed; private diagnostics withheld': 'fixture_target_route_read_failed',
  'decode local target route failed; private diagnostics withheld': 'fixture_target_route_decode_failed',
  'target does not have its exact local loopback route': 'fixture_target_route_invalid',
  'bind exact owned target failed; private diagnostics withheld': 'fixture_target_bind_failed',
  'native setup stage: capability\n': 'global_setup_stage_capability',
  'native setup stage: group\n': 'global_setup_stage_group',
  'native setup stage: principal\n': 'global_setup_stage_principal',
  'native setup stage: membership\n': 'global_setup_stage_membership',
  'native setup stage: policy\n': 'global_setup_stage_policy',
  'native setup stage: mode\n': 'global_setup_stage_mode',
  'native setup stage: collection\n': 'global_setup_stage_collection',
  'native setup stage: publication\n': 'global_setup_stage_publication',
  'native setup stage: receipt\n': 'global_setup_stage_receipt',
  'test timed out after 20m': 'global_batch_suite_timeout',
  'nativePrepareTrialLoad(': 'global_batch_setup_stack',
  '(*Service).ResyncMembership(': 'global_batch_membership_stack',
  'internal/service/sharedclient.(*Service).ProvisionClient(': 'global_batch_provision_stack',
  'internal/service/nodesync.(*Service).NativePanelSnapshot(': 'global_batch_snapshot_stack',
  'internal/service/nodesync.(*Service).NativePanelClientSnapshot(': 'global_batch_scoped_snapshot_stack',
  'internal/adapters/sqlstore.(*nativeDesiredRepo).Load(': 'global_batch_desired_sql_stack',
  'internal/adapters/sqlstore.(*nativeDesiredRepo).LoadClient(': 'global_batch_scoped_desired_sql_stack',
  'internal/adapters/sqlstore.(*nativeDesiredRepo).load(': 'global_batch_desired_sql_internal_stack',
  'internal/adapters/sqlstore.(*pspClientRepo).ListInbounds(': 'global_batch_attachment_read_stack',
  'internal/adapters/sqlstore.(*pspClientRepo).UpdateInboundStates(': 'global_batch_attachment_write_stack',
  'internal/adapters/sqlstore.(*nodeAgentRepo).RecordApplied(': 'global_batch_stream_ack_stack',
  'internal/adapters/sqlstore.(*nodeAgentRepo).RecordAppliedBatch(': 'global_batch_stream_ack_vector_stack',
  'internal/adapters/sqlstore.(*nodeAgentRepo).UpdateProtocolObservation(': 'global_batch_protocol_write_stack',
  'internal/adapters/sqlstore.(*nodeAgentRepo).MintConfigWithPolicyCandidate(': 'global_batch_config_mint_stack',
  'internal/adapters/sqlstore.(*nodeAgentIssueRepo).RecordBatch(': 'global_batch_issues_write_stack',
  'database/sql.(*DB).conn(': 'global_batch_sql_connection_stack',
  'global batch readiness: state': 'global_batch_readiness_state',
  'global batch readiness: store': 'global_batch_readiness_store',
  'global batch readiness: capability': 'global_batch_readiness_capability',
  'global batch readiness: streams_store': 'global_batch_readiness_streams_store',
  'global batch readiness: streams_metadata': 'global_batch_readiness_streams_metadata',
  'global batch readiness: streams_config': 'global_batch_readiness_streams_config',
  'global batch readiness: streams_roster': 'global_batch_readiness_streams_roster',
  'global batch readiness: streams_adapter': 'global_batch_readiness_streams_adapter',
  'global batch readiness: streams_inbound_missing': 'global_batch_readiness_streams_inbound_missing',
  'global batch readiness: streams_inbound_offline': 'global_batch_readiness_streams_inbound_offline',
  'global batch readiness: streams_inbound_stale': 'global_batch_readiness_streams_inbound_stale',
  'global batch readiness: streams_inbound_read': 'global_batch_readiness_streams_inbound_read',
  'global batch readiness: streams_inbound_absent': 'global_batch_readiness_streams_inbound_absent',
  'global batch readiness: streams_inbound_runtime': 'global_batch_readiness_streams_inbound_runtime',
  'global batch readiness: streams_inbound_shape': 'global_batch_readiness_streams_inbound_shape',
  'global batch readiness: streams_inbound': 'global_batch_readiness_streams_inbound',
  'global batch readiness: streams': 'global_batch_readiness_streams',
  'global batch readiness: receipt': 'global_batch_readiness_receipt',
  'global batch readiness: collection': 'global_batch_readiness_collection',
  'global batch readiness: shape': 'global_batch_readiness_shape',
  'global batch readiness: subjects': 'global_batch_readiness_subjects',
  'global batch readiness: roster': 'global_batch_readiness_roster',
  'global batch readiness: complete': 'global_batch_readiness_complete',
  'fleet administrator returned HTTP': 'fleet_admin_http_failed',
  'timed out waiting for sing-box restarted runtime and inbound': 'singbox_restart_runtime_timeout',
  'timed out waiting for exact load policy and collection receipt': 'load_receipt_timeout',
  'block overflow never exhausted its independent actual budget': 'block_overflow_budget_failed',
  'read actual exhausted block reservation': 'block_overflow_budget_read_failed',
  'timed out waiting for actual native trial provisioning capability': 'trial_load_capability_timeout',
  'timed out waiting for current trial policy and complete real roster': 'trial_load_roster_timeout',
  'timed out waiting for actual trial saturation with intact block ingestion': 'trial_load_ingestion_timeout',
  'trial flood lost sustained delivery, anonymity, control or independent block reservation': 'trial_load_counts_failed',
  'trial or independent block flow fell below its actual rate': 'trial_load_rate_failed',
  'trial principal escaped into private candidate output': 'trial_load_privacy_failed',
  'timed out waiting for every individual fleet receipt': 'fleet_receipt_timeout',
  'timed out waiting for actual fleet member removal and restoration': 'fleet_membership_timeout',
  'fleet real traffic did not match individual membership and policy': 'fleet_traffic_failed',
  'fleet public coverage borrowed capability from another panel': 'fleet_coverage_failed',
  'start immutable real third-party fleet backends': 'fleet_backends_failed',
  'fleet public status borrowed another node': 'fleet_public_status_failed',
  'fleet public status omitted or borrowed an owned peer': 'fleet_public_status_failed',
  'third-party fleet status falsely acquired native execution': 'fleet_public_status_failed',
  'fleet reopen replaced a retired client identity or counters': 'fleet_identity_failed',
  'fleet panel is not the exact reviewed version': 'fleet_version_failed',
  'fleet backend changed its reviewed image': 'fleet_provenance_failed',
  'Docker automatic restoration changed original receipt identity': 'docker_restore_receipt_failed',
  'Docker automatic restoration changed original execution identity': 'docker_restore_execution_failed',
  'Docker automatic restoration changed original installation identity': 'docker_restore_installation_failed',
  'Docker restored failure retained authenticated readiness': 'docker_restore_ready_failed',
  'timed out waiting for Docker failed result acknowledgement': 'docker_failed_ack_timeout',
  'timed out waiting for Docker successful result acknowledgement': 'docker_success_ack_timeout',
  'Docker replacement lost private material, original state inode or advancing counter epoch': 'docker_preservation_failed',
  'Docker replacement changed stable client identity or cumulative counters': 'docker_client_preservation_failed',
  'Docker real traffic disagreed with actual policy execution': 'docker_traffic_failed',
  'Docker health failure did not return a determinate failed result': 'docker_failed_task_invalid',
  'Docker failed activation lost its original durable claim': 'docker_activation_claim_failed',
  'Docker target activation did not bind the real replacement': 'docker_activation_identity_failed',
  'Docker rejected target produced authenticated readiness': 'docker_rejected_ready_failed',
  'Docker official rollback task failed': 'docker_rollback_task_failed',
  'Docker public status did not verify the actual installed release': 'docker_public_status_failed',
  'Docker success receipt did not retain the authorized execution': 'docker_success_receipt_failed',
  'Docker readiness did not bind the current authorized activation': 'docker_ready_failed',
  'Docker target did not recover and acknowledge its original claim': 'docker_success_claim_failed',
  'Docker updater changed its production mount isolation': 'docker_mount_profile_failed',
  'Docker installed image has no official registry digest': 'docker_registry_digest_failed',
  'Docker readiness namespace PID did not bind the current host process': 'docker_namespace_failed',
  'Docker managed state is not schema9 with a durable counter epoch': 'docker_state_failed',
  'Docker credential escaped into process arguments or environment': 'docker_process_privacy_failed',
  'Docker cleanup refused changed ownership provenance': 'docker_cleanup_owner_failed',
  'Docker administrator returned HTTP': 'docker_admin_http_failed',
  'Docker acceptance command failed': 'docker_command_failed',
  'Docker deployment changed the production isolation profile': 'docker_profile_failed',
  'Docker enrollment did not retain the fixture identity and TLS endpoint': 'docker_enrollment_failed',
  'Docker agent did not drop to its production non-root identity': 'docker_uid_failed',
  'Docker agent has no matching owned live core': 'docker_core_missing',
  'timed out waiting for Docker current candidate policy': 'docker_candidate_timeout',
  'timed out waiting for Docker rejected target activation': 'docker_activation_timeout',
  'timed out waiting for Docker automatic health restoration': 'docker_auto_restore_timeout',
  'timed out waiting for Docker published helper completion': 'docker_helper_timeout',
  'timed out waiting for Docker restart production convergence': 'docker_restart_timeout',
  'Docker installed image is not the actual official registry replacement': 'docker_image_failed',
  'Docker logs exposed private traffic or credentials': 'docker_privacy_failed',
  'Docker cleanup provenance check failed': 'docker_cleanup_failed',
  'timed out waiting for remote failed target activation': 'remote_failed_activation_timeout',
  'timed out waiting for remote automatic health failure restoration': 'remote_auto_restore_timeout',
  'remote failed target activation evidence invalid': 'remote_failed_activation_invalid',
  'remote rejected target produced authenticated readiness': 'remote_false_readiness',
  'remote failed activation lacks the original running execution': 'remote_failed_execution_invalid',
  'remote health failure did not return a determinate failed task': 'remote_failed_outcome_invalid',
  'remote automatic restoration receipt invalid': 'remote_restore_receipt_invalid',
  'remote automatic restoration changed durable execution identity': 'remote_restore_execution_invalid',
  'remote automatic restoration did not confirm the source release': 'remote_restore_source_invalid',
  'remote automatic restoration lost stable client counters or identity': 'remote_restore_client_invalid',
  'remote failed activation left authenticated readiness': 'remote_failed_ready_retained',
  'timed out waiting for remote candidate nonempty policy': 'remote_candidate_timeout',
  'timed out waiting for remote candidate raw counters': 'remote_raw_counters_timeout',
  'timed out waiting for remote candidate cumulative counters': 'remote_counters_timeout',
  'fold real remote traffic through production collector': 'remote_collector_failed',
  'remote acceptance command failed': 'remote_command_failed',
  'timed out waiting for published remote helper completion': 'remote_helper_timeout',
  'timed out waiting for published remote rollback convergence': 'remote_convergence_timeout',
  'published remote helper task failed': 'remote_task_failed',
  'remote public status did not confirm the actual published target': 'remote_status_failed',
  'remote rollback replaced private identity': 'remote_identity_failed',
  'published helper receipt is not the completed authorized task': 'remote_receipt_failed',
  'remote target readiness did not bind the current activation': 'remote_ready_failed',
  'remote target did not durably recover and acknowledge the original execution': 'remote_recovery_failed',
  'remote restart changed the immutable completed execution': 'remote_restart_failed',
  'remote completed task replay executed the helper again': 'remote_replay_failed',
  'remote withdrawal lost policy history': 'remote_history_failed',
  'private remote traffic escaped into journal': 'remote_privacy_failed',
  'remote cleanup provenance check failed': 'remote_cleanup_failed',
  'timed out waiting for native scoped policy application': 'scope_policy_timeout',
  'timed out waiting for native scoped hit ingestion': 'scope_hits_timeout',
  'timed out waiting for native exemption clock expiry': 'scope_expiry_timeout',
  'exempt traffic produced a block hit': 'scope_exempt_hit_failed',
  'unscoped peer traffic produced a block hit': 'scope_group_hit_failed',
  'allow priority produced a block hit': 'scope_allow_hit_failed',
  'scoped membership changed original credentials': 'scope_identity_changed',
  'timed out waiting for real allowlist capability membership convergence': 'capability_membership_timeout',
  'allowlist capability traffic did not match actual membership': 'capability_membership_traffic_failed',
  'allowlist capability transition changed original identity': 'capability_identity_changed',
  'allowlist capability coverage borrowed a peer eligibility': 'capability_coverage_invalid',
  'timed out waiting for requested current collection receipt': 'collection_receipt_timeout',
  'native collection input did not retain its requested revision': 'collection_input_invalid',
  'timed out waiting for released panel restart ready': 'panel_restart_timeout',
  'timed out waiting for panel downgrade offline attempts': 'panel_offline_timeout',
  'timed out waiting for online PSP downgrade application': 'panel_downgrade_timeout',
  'timed out waiting for PSP reupgrade current policy execution': 'panel_reupgrade_timeout',
  'panel rollback requires a published executing policy': 'panel_original_unconfirmed',
  'panel rollback did not bind the complete destination schema': 'panel_schema_incomplete',
  'offline panel downgrade changed its live artifact or destination data': 'panel_offline_preservation_failed',
  'online panel downgrade did not deploy the original policy-free artifact': 'panel_empty_artifact_failed',
  'released panel modified destination tables or data': 'panel_destination_preservation_failed',
  'PSP reupgrade lost the published policy state': 'panel_publication_lost',
  'panel transition traffic did not match actual execution': 'panel_traffic_failed',
  'panel transition replaced an original database or identity': 'panel_identity_changed',
  'PSP reupgrade coverage did not confirm current execution': 'panel_coverage_failed',
  'timed out waiting for original node binary transition': 'node_transition_timeout',
  'original node transition traffic did not match execution': 'node_transition_traffic_failed',
  'node binary transition replaced its state database or changed schema': 'node_transition_state_invalid',
  'node binary transition lost its original principal or identity': 'node_transition_identity_invalid',
  'node downgrade did not restore the original core artifact': 'node_downgrade_artifact_changed',
  'node downgrade retained executable policy in the current candidate': 'node_downgrade_policy_dispatched',
  'node downgrade erased confirmed policy history': 'node_downgrade_history_erased',
  'node transition coverage did not match its own execution': 'node_transition_coverage_invalid',
  'timed out waiting for mixed current candidate application': 'mixed_apply_timeout',
  'timed out waiting for mixed single-node offline pause': 'mixed_offline_timeout',
  'mixed candidate real traffic did not match its execution state': 'mixed_traffic_failed',
  "mixed coverage borrowed a peer's policy capability or execution": 'mixed_coverage_invalid',
  'mixed candidate did not confirm its own applied rule': 'mixed_candidate_unconfirmed',
  'mixed offline node falsely stopped execution': 'mixed_offline_state_invalid',
  'mixed reconnected node did not apply the pause': 'mixed_pause_unconfirmed',
  'mixed legacy node acquired policy execution material': 'mixed_legacy_policy_invalid',
  'legacy upgrade admin dest/policies returned HTTP 400': 'legacy_policy_input_rejected',
  'legacy upgrade admin dest/policies returned HTTP 401': 'legacy_policy_auth_failed',
  'legacy upgrade admin dest/policies returned HTTP 403': 'legacy_policy_access_failed',
  'legacy upgrade admin dest/publish returned HTTP 409': 'legacy_publish_conflict',
  'timed out waiting for released panel ready': 'legacy_panel_start_timeout',
  'timed out waiting for released node applied released panel': 'legacy_node_apply_timeout',
  'timed out waiting for unchanged legacy upgrade sync': 'legacy_unchanged_sync_timeout',
  'legacy database already contained destination schema': 'legacy_schema_fixture_invalid',
  'PSP upgrade changed legacy stream validators or dispatched policy': 'legacy_upgrade_stream_changed',
  'PSP upgrade rewrote the legacy artifact': 'legacy_upgrade_artifact_changed',
  'legacy node coverage did not require an upgrade': 'legacy_upgrade_status_invalid',
  'legacy node received a policy runtime candidate': 'legacy_upgrade_candidate_invalid',
  'sniffing precheck did not persist its operator issue': 'sniffing_issue_missing',
  'timed out waiting for reviewed core switch': 'fallback_core_switch_timeout',
  'mapped-prefix LKG was not actually confirmed by sing-box': 'fallback_original_unconfirmed',
  'fallback rejection fixture did not distinguish pruning from desired': 'fallback_rejection_fixture_invalid',
  'real rejected fallback report did not match the exact minted policy': 'fallback_rejection_identity_failed',
  'timed out waiting for rejected fallback empty deployment': 'fallback_empty_timeout',
  'status API did not confirm exhausted rejected fallback': 'fallback_exhausted_status_failed',
  'timed out waiting for rejected fallback roster revocation': 'fallback_rejected_roster_timeout',
  'roster mutation rearmed rejected fallback': 'fallback_rejected_roster_rearmed',
  'PSP restart rearmed rejected pruned fallback': 'fallback_rejected_restart_rearmed',
  'exhausted fallback retry was not admitted': 'fallback_rejected_retry_failed',
  'timed out waiting for exhausted fallback manual retry rejection': 'fallback_rejected_retry_timeout',
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
const fallbackStages = new Set(['collection_off', 'compatible_listener', 'singbox_selection', 'policy_create', 'original_publication', 'original_policy', 'original_traffic', 'partitioned_mutation', 'mutation_publication', 'xray_selection', 'empty_recovery', 'recovered_traffic', 'roster_revocation', 'revoked_traffic', 'psp_shutdown', 'psp_reopen', 'psp_restart_receipt', 'psp_restart_traffic', 'manual_retry', 'restore_fixture'])
function isSafeFallbackFact(name, value) {
  return Number.isInteger(value) && (name === 'error_kind' ? value >= 1 && value <= 4 : name === 'mysql_code' && value >= 0 && value <= 65535)
}
export function sanitizeDestinationResults(input) {
  return input.split(/\r?\n/).filter(line => line.trim()).map(line => {
    const event = parseEvent(line)
    if (event.Action === 'output') {
      const fallbackStage = typeof event.Output === 'string' && /native fallback stage: ([a-z_]+)(?:\r?\n|$)/.exec(event.Output)
      if (fallbackStage && fallbackStages.has(fallbackStage[1])) return JSON.stringify({ Action: 'output', Package: pkg, Diagnostic: `fallback_stage_${fallbackStage[1]}` })
      const fallbackFact = typeof event.Output === 'string' && /native fallback failure: (error_kind|mysql_code)=([0-9]{1,5})(?:\r?\n|$)/.exec(event.Output)
      if (fallbackFact && isSafeFallbackFact(fallbackFact[1], Number(fallbackFact[2]))) return JSON.stringify({ Action: 'output', Package: pkg, Diagnostic: `fallback_${fallbackFact[1]}`, Observed: Number(fallbackFact[2]) })
      const adminHTTP = typeof event.Output === 'string' && /native admin HTTP: (actual|expected)=([1-5][0-9]{2})(?:\r?\n|$)/.exec(event.Output)
      if (adminHTTP) return JSON.stringify({ Action: 'output', Package: pkg, Diagnostic: `admin_http_${adminHTTP[1]}`, Observed: Number(adminHTTP[2]) })
      const fixtureRoute = typeof event.Output === 'string' && /native diagnostic: (fixture_route_(?:one|local|loopback|exact_address|exact_host_prefix))=(true|false)(?:\r?\n|$)/.exec(event.Output)
      if (fixtureRoute) return JSON.stringify({ Action: 'output', Package: pkg, Diagnostic: fixtureRoute[1], Observed: fixtureRoute[2] === 'true' })
      const configError = typeof event.Output === 'string' && /native global config error: ((?:catalog|build|compile|repository|capability|encode|mint)_(?:canceled|deadline|conflict|validation|unavailable|other))=([0-9]{1,7})(?:\r?\n|$)/.exec(event.Output)
      if (configError && isSafeGlobalConfigError(configError[1], Number(configError[2]))) return JSON.stringify({ Action: 'output', Package: pkg, Diagnostic: `global_config_error_${configError[1]}`, Observed: Number(configError[2]) })
      const dockerPreserved = typeof event.Output === 'string' && /native diagnostic: (docker_preserved_material|docker_preserved_state|docker_advanced_epoch)=(true|false)(?:\r?\n|$)/.exec(event.Output)
      if (dockerPreserved) return JSON.stringify({ Action: 'output', Package: pkg, Diagnostic: dockerPreserved[1], Observed: dockerPreserved[2] === 'true' })
      const globalStage = typeof event.Output === 'string' && /native global stage: ((?:desired|ingest|policy|panel|config|roster|directives|tasks|finish)_(?:count|mean_ms|p95_ms|max_ms))=([0-9]{1,7})(?:\r?\n|$)/.exec(event.Output)
      if (globalStage && isSafeGlobalStageFact(globalStage[1], Number(globalStage[2]))) return JSON.stringify({ Action: 'output', Package: pkg, Diagnostic: `global_stage_${globalStage[1]}`, Observed: Number(globalStage[2]) })
      const globalSync = typeof event.Output === 'string' && /native global sync: (attempt_age_ms|completion_age_ms|last_duration_ms|last_status|inflight|parent_alive)=([0-9]{1,7})(?:\r?\n|$)/.exec(event.Output)
      if (globalSync && isSafeGlobalSyncFact(globalSync[1], Number(globalSync[2]))) return JSON.stringify({ Action: 'output', Package: pkg, Diagnostic: `global_sync_${globalSync[1]}`, Observed: Number(globalSync[2]) })
      const bytePhase = typeof event.Output === 'string' && /native global byte phase: (priming_ms|first_sql_ms|formation_ms|live_keys_ms)=([0-9]{1,7})(?:\r?\n|$)/.exec(event.Output)
      if (bytePhase && isSafeGlobalBytePhaseFact(bytePhase[1], Number(bytePhase[2]))) return JSON.stringify({ Action: 'output', Package: pkg, Diagnostic: `global_byte_phase_${bytePhase[1]}`, Observed: Number(bytePhase[2]) })
      const setupCPU = typeof event.Output === 'string' && /native global setup cpu: (sample|elapsed_ms|sample_ms|parent_ticks|waited_child_ticks|live_core_ticks|changed_core_identities|parents|cores|ticks_per_second|logical_cpus|unavailable)=([0-9]{1,13})(?:\r?\n|$)/.exec(event.Output)
      if (setupCPU && isSafeSetupCPUFact(setupCPU[1], Number(setupCPU[2]))) return JSON.stringify({ Action: 'output', Package: pkg, Diagnostic: `global_setup_cpu_${setupCPU[1]}`, Observed: Number(setupCPU[2]) })
      const drain = typeof event.Output === 'string' && /native global drain: ([a-z_]+)=([0-9]{1,13})(?:\r?\n|$)/.exec(event.Output)
      if (drain && isSafeGlobalDrainFact(drain[1], Number(drain[2]))) return JSON.stringify({ Action: 'output', Package: pkg, Diagnostic: `global_drain_${drain[1]}`, Observed: Number(drain[2]) })
      const traffic = typeof event.Output === 'string' && /native global traffic: ([a-z_]+)=([0-9]{1,7})(?:\r?\n|$)/.exec(event.Output)
      if (traffic && isSafeGlobalTrafficFact(traffic[1], Number(traffic[2]))) return JSON.stringify({ Action: 'output', Package: pkg, Diagnostic: `global_traffic_${traffic[1]}`, Observed: Number(traffic[2]) })
      const requestFailure = typeof event.Output === 'string' && /native global request: ([a-z_]+)=([0-9]{1,7})(?:\r?\n|$)/.exec(event.Output)
      if (requestFailure && isSafeGlobalRequestFact(requestFailure[1], Number(requestFailure[2]))) return JSON.stringify({ Action: 'output', Package: pkg, Diagnostic: `global_request_${requestFailure[1]}`, Observed: Number(requestFailure[2]) })
      const setupProfile = typeof event.Output === 'string' && /native global setup profile: (elapsed_ms|allocated_bytes|gc_cycles|gc_pause_ns|heap_bytes)=([0-9]{1,16})(?:\r?\n|$)/.exec(event.Output)
      if (setupProfile && isSafeSetupProfileFact(setupProfile[1], Number(setupProfile[2]))) return JSON.stringify({ Action: 'output', Package: pkg, Diagnostic: `global_setup_profile_${setupProfile[1]}`, Observed: Number(setupProfile[2]) })
      const fleetSetup = typeof event.Output === 'string' && /native diagnostic: (fleet_setup_(?:cohort|candidates|database|session|cadence|principals|peers|server|daemons|membership))=(true|false)(?:\r?\n|$)/.exec(event.Output)
      if (fleetSetup) return JSON.stringify({ Action: 'output', Package: pkg, Diagnostic: fleetSetup[1], Observed: fleetSetup[2] === 'true' })
      const loadReceipt = typeof event.Output === 'string' && /native diagnostic: (load_(?:state|runtime|agent|panel|selection|observed_core|transition|collection|rule_count|rule_shape|config|roster))=(true|false)(?:\r?\n|$)/.exec(event.Output)
      if (loadReceipt) return JSON.stringify({ Action: 'output', Package: pkg, Diagnostic: loadReceipt[1], Observed: loadReceipt[2] === 'true' })
      const trialSetup = typeof event.Output === 'string' && /native diagnostic: (trial_setup_(?:capability|group|principal|membership|policy|mode|collection|publication|receipt|resources|warmup|traffic|ingestion))=(true|false)(?:\r?\n|$)/.exec(event.Output)
      if (trialSetup) return JSON.stringify({ Action: 'output', Package: pkg, Diagnostic: trialSetup[1], Observed: trialSetup[2] === 'true' })
      const statusFact = typeof event.Output === 'string' && /native diagnostic: status_(rows|panel|kind|reason|exhausted|rules|pending|applied_at|state|paused|generation|is_applied|is_sniffing|is_paused|is_pending|is_offline|is_none|is_rejected|is_over_limit)=(true|false)(?:\r?\n|$)/.exec(event.Output)
      if (statusFact) return JSON.stringify({ Action: 'output', Package: pkg, Diagnostic: `status_${statusFact[1]}`, Observed: statusFact[2] === 'true' })
      const category = typeof event.Output === 'string' && /native diagnostic: (fault_body_present|fault_body_valid|fault_body_digest|fault_body_rules|fault_body_collect|fleet_backend_sui_running|fleet_backend_sui_bind_error|fleet_backend_sui_storage_error|fleet_backend_sui_start_error|fleet_runtime_present|fleet_prior_present|fleet_kind_matches|fleet_generation_matches|fleet_minted_present|fleet_reported_present|fleet_report_state|fleet_report_sha|fleet_heartbeat_fresh|fleet_mint_fresh|fleet_exact_receipt|fleet_backend_xui_ready|fleet_backend_sui_ready|fleet_backend_xui_pull|fleet_backend_sui_pull|fleet_backend_auth_error|fleet_backend_timeout|fleet_backend_registry_error|fleet_backend_sql_error|fleet_peer_[0-7]_(?:observation|agent|heartbeat|epoch|capability|selection|engine|version|running|listener|legacy|streams|policy|download|checksum|timeout|rejected|unsupported|permission|bind|certificate)|download|checksum|timeout|rejected|unsupported|permission|bind|certificate|listener_issue|listener_core_rejected|listener_agent_fresh|listener_policy_ready|listener_policy_unchanged|listener_epoch|listener_config_changed|listener_config_preserved|listener_config_fresh|receipt_generation|receipt_mint_fresh|receipt_sync_fresh|receipt_state|receipt_sha|receipt_lkg|receipt_precheck|receipt_streams|receipt_transition|fallback_report|fallback_accepted|fallback_empty|fallback_exhausted|fallback_core|fallback_streams|fallback_issue|node_transition_sequence|node_transition_capability|node_transition_policy|node_transition_core|node_transition_listener|node_transition_config|node_transition_roster|node_transition_version|node_transition_original_etags|node_transition_agent_fresh|node_transition_streams|node_transition_minted_empty|panel_streams|panel_capability|panel_config_known|panel_config_policy|panel_report_applied|panel_report_empty|panel_report_current|panel_original_core_exited|capability_streams|capability_capability|capability_agent_fresh|capability_membership|capability_policy_body|capability_policy_receipt|remote_restore_task_terminal|remote_restore_receipt_failed|remote_restore_receipt_activated|remote_restore_source_installed|remote_restore_seen|remote_restore_identity|remote_restore_capability|remote_restore_streams|remote_restore_policy|remote_restore_report|remote_restore_core|remote_restore_listener|remote_restore_journal_readable|remote_restore_journal_failed|remote_restore_journal_running|remote_restore_journal_delivered|remote_restore_quarantined|remote_restore_failed_result_posted|remote_restore_failed_result_accepted|remote_restore_failed_result_rejected|remote_restore_outbox_pending|remote_restore_outbox_valid|remote_restore_fresh_after_restore|remote_restore_sync_validation_error|remote_restore_sync_outbox_error|remote_restore_sync_observer_error|remote_restore_sync_duplicate_issue|remote_restore_sync_stream_error|remote_restore_sync_process_error)=(true|false)(?:\r?\n|$)/.exec(event.Output)
      if (category) return JSON.stringify({ Action: 'output', Package: pkg, Diagnostic: category[1], Observed: category[2] === 'true' })
      const sequence = typeof event.Output === 'string' && /native receipt wait: ([1-9][0-9]?)(?:\r?\n|$)/.exec(event.Output)
      if (sequence) return JSON.stringify({ Action: 'output', Package: pkg, Diagnostic: 'receipt_wait', Observed: Number(sequence[1]) })
      const application = typeof event.Output === 'string' && /native panel application: (downgrade|reupgrade)=([0-9]{1,6})(?:\r?\n|$)/.exec(event.Output)
      if (application && Number(application[2]) <= 150000) return JSON.stringify({ Action: 'output', Package: pkg, Diagnostic: `panel_${application[1]}_application_ms`, Observed: Number(application[2]) })
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
export function checkLegacyUpgradeResults(input) { checkCases(input, legacyUpgradeNames) }
export function checkFleetResults(input) { checkCases(input, fleetNames) }
export function checkDestinationLoadResults(input) { checkCases(input, loadNames) }
export function checkDestinationTrialLoadResults(input) { checkCases(input, trialLoadNames) }
export function checkDestinationMixedPressureResults(input) { checkCases(input, mixedPressureNames) }
export function checkDestinationIdleCostResults(input) { checkCases(input, idleCostNames) }
export function checkDestinationGlobalBootstrapResults(input) { checkCases(input, globalBootstrapNames) }
export function checkDestinationGlobalBatchResults(input) { checkCases(input, globalBatchNames) }
export function checkDestinationGlobalByteResults(input) { checkCases(input, globalByteNames) }
export function checkDestinationBlockOverflowResults(input) { checkCases(input, blockOverflowNames) }
export function checkRemoteUpgradeResults(input) { checkCases(input, remoteUpgradeNames) }
export function checkDockerUpgradeResults(input) { checkCases(input, dockerUpgradeNames) }
export function isSafeDestinationDiagnostic(event) {
  if (typeof event.Diagnostic !== 'string') return false
  if (event.Observed === undefined) return Object.values(diagnostics).includes(event.Diagnostic) || event.Diagnostic.startsWith('fallback_stage_') && fallbackStages.has(event.Diagnostic.slice('fallback_stage_'.length))
  if (typeof event.Observed === 'boolean') {
    const roundTrip = sanitizeDestinationResults(JSON.stringify({ Action: 'output', Output: `native diagnostic: ${event.Diagnostic}=${event.Observed}\n` }))
    if (!roundTrip.trim()) return false
    const sanitized = JSON.parse(roundTrip)
    return sanitized.Diagnostic === event.Diagnostic && sanitized.Observed === event.Observed
  }
  if (!Number.isInteger(event.Observed) || event.Observed < 0) return false
  if (['fallback_error_kind', 'fallback_mysql_code'].includes(event.Diagnostic)) return isSafeFallbackFact(event.Diagnostic.slice('fallback_'.length), event.Observed)
  if (['admin_http_actual', 'admin_http_expected'].includes(event.Diagnostic)) return event.Observed >= 100 && event.Observed <= 599
  if (event.Diagnostic.startsWith('global_setup_profile_')) return isSafeSetupProfileFact(event.Diagnostic.slice('global_setup_profile_'.length), event.Observed)
  if (event.Diagnostic.startsWith('global_stage_')) return isSafeGlobalStageFact(event.Diagnostic.slice('global_stage_'.length), event.Observed)
  if (event.Diagnostic.startsWith('global_config_error_')) return isSafeGlobalConfigError(event.Diagnostic.slice('global_config_error_'.length), event.Observed)
  if (event.Diagnostic.startsWith('global_sync_')) return isSafeGlobalSyncFact(event.Diagnostic.slice('global_sync_'.length), event.Observed)
  if (event.Diagnostic.startsWith('global_byte_phase_')) return isSafeGlobalBytePhaseFact(event.Diagnostic.slice('global_byte_phase_'.length), event.Observed)
  if (event.Diagnostic.startsWith('global_setup_cpu_')) return isSafeSetupCPUFact(event.Diagnostic.slice('global_setup_cpu_'.length), event.Observed)
  if (event.Diagnostic.startsWith('global_drain_')) return isSafeGlobalDrainFact(event.Diagnostic.slice('global_drain_'.length), event.Observed)
  if (event.Diagnostic.startsWith('global_traffic_')) return isSafeGlobalTrafficFact(event.Diagnostic.slice('global_traffic_'.length), event.Observed)
  if (event.Diagnostic.startsWith('global_request_')) return isSafeGlobalRequestFact(event.Diagnostic.slice('global_request_'.length), event.Observed)
  if (event.Diagnostic === 'receipt_wait') return event.Observed >= 1 && event.Observed <= 99
  return ['panel_downgrade_application_ms', 'panel_reupgrade_application_ms'].includes(event.Diagnostic) && event.Observed <= 150000
}

function isSafeGlobalBytePhaseFact(name, value) {
  return ['priming_ms', 'first_sql_ms', 'formation_ms', 'live_keys_ms'].includes(name) && Number.isSafeInteger(value) && value >= 0 && value <= 1200000
}

function isSafeGlobalTrafficFact(name, value) {
  return (name === 'total_failures' || /^(block|observe|trial|usage|control)_(queue|request)_failures$/.test(name)) && Number.isSafeInteger(value) && value >= 0 && value <= 1000000
}

function isSafeGlobalRequestFact(name, value) {
  return /^(block|observe|trial|usage|control)_(dial_timeout|write_timeout|read_timeout|timeout|refused|reset|address_unavailable|resource_limit|eof|short_write|other)$/.test(name) && Number.isSafeInteger(value) && value >= 0 && value <= 1000000
}

function isSafeGlobalDrainFact(name, value) {
  if (!Number.isSafeInteger(value) || value < 0) return false
  if (name === 'http_failure_count') return value >= 1 && value <= 1000000
  // Zero records a handler that never explicitly wrote a status or body.
  if (name === 'last_http_failure_status') return value <= 999
  if (name === 'owner') return value >= 1 && value <= 65
  if (name === 'lane') return value >= 1 && value <= 4
  if (name === 'owners_finished') return value <= 65
  if (name === 'node_lanes') return value <= 4
  if (name === 'node_invalid_causes') return value <= 127
  if (['node_invalid', 'accounted_negative'].includes(name)) return value <= 1
  if (['traffic_ms', 'sample_first_ms', 'sample_last_ms'].includes(name)) return value <= 1200000
  const accessField = /^(before|after)_(private|usage|denied|unmatched|dropped|stale|pending_dropped|pending_unmatched|queued)$/.test(name)
  return (accessField || ['samples', 'frozen', 'retried', 'delivered_allowed', 'delivered_block', 'expected', 'accounted', 'node_keys', 'node_pending', 'node_dropped', 'node_unmatched', 'queue_batches'].includes(name)) && value <= 1000000000000
}

function isSafeSetupCPUFact(name, value) {
  if (!Number.isSafeInteger(value) || value < 0) return false
  if (name === 'sample') return value >= 1 && value <= 10
  if (['elapsed_ms', 'sample_ms'].includes(name)) return value <= 1200000
  if (['parent_ticks', 'waited_child_ticks', 'live_core_ticks'].includes(name)) return value <= 1000000000000
  if (['parents', 'cores', 'changed_core_identities'].includes(name)) return value <= 65
  if (name === 'ticks_per_second') return value >= 1 && value <= 1000000
  if (name === 'logical_cpus') return value >= 1 && value <= 4096
  return name === 'unavailable' && value === 1
}

function isSafeSetupProfileFact(name, value) {
  const limits = { elapsed_ms: 1200000, allocated_bytes: 1000000000000000, gc_cycles: 1000000, gc_pause_ns: 1200000000000, heap_bytes: 1000000000000000 }
  return Object.hasOwn(limits, name) && Number.isSafeInteger(value) && value >= 0 && value <= limits[name]
}

function isSafeGlobalSyncFact(name, value) {
  if (!Number.isInteger(value) || value < 0) return false
  if (['attempt_age_ms', 'completion_age_ms', 'last_duration_ms'].includes(name)) return value <= 1200000
  if (name === 'last_status') return value >= 100 && value <= 599
  if (name === 'inflight') return value <= 65
  return name === 'parent_alive' && value <= 1
}

function isSafeGlobalStageFact(name, value) {
  if (!/^(desired|ingest|policy|panel|config|roster|directives|tasks|finish)_(count|mean_ms|p95_ms|max_ms)$/.test(name) || !Number.isInteger(value) || value < 0) return false
  return name.endsWith('_count') ? value >= 1 && value <= 1000000 : value <= 1200000
}
function isSafeGlobalConfigError(name, value) {
  return /^(catalog|build|compile|repository|capability|encode|mint)_(canceled|deadline|conflict|validation|unavailable|other)$/.test(name) && Number.isInteger(value) && value >= 1 && value <= 1000000
}
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
    const checks = { check: checkDestinationResults, 'check-singbox': checkSingBoxResults, 'check-guarded-singbox': checkGuardedSingBoxResults, 'check-legacy-upgrade': checkLegacyUpgradeResults, 'check-remote-upgrade': checkRemoteUpgradeResults, 'check-docker-upgrade': checkDockerUpgradeResults, 'check-fleet': checkFleetResults }
    if (!Object.hasOwn(checks, process.argv[2]) || process.argv.length !== 4) throw new Error('Usage: destination-acceptance.mjs check|check-singbox|check-guarded-singbox|check-legacy-upgrade|check-remote-upgrade|check-docker-upgrade|check-fleet RESULTS')
    const check = checks[process.argv[2]]
    check(readFileSync(process.argv[3], 'utf8'))
    console.log('Every native destination acceptance case ran and passed')
  }
}
