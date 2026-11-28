import { useState, useSyncExternalStore } from 'react'
import { useQueryClient, type QueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import {
  deleteServer, requestNativeAgentUpgrade, upgradeOptions, upgradePanel, upgradePreview, upgradeXray, type Server, type UpgradePreviewResult,
} from '@/api/servers'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/feedback/toasts'
import { allSettledLimited } from '@/utils/promises'

// Which servers have an upgrade or batch action in flight. The list's batch buttons and a server's own page are different
// hook instances, and each must see what the other started, so the state lives with the query client they share
// (one per signed-in session) rather than in either component.
interface Busy { counts: Map<number, number>; watchers: Set<() => void>; snapshot: ReadonlySet<number> }
const busyOf = new WeakMap<QueryClient, Busy>()
function busyFor(client: QueryClient): Busy {
  let b = busyOf.get(client)
  if (!b) { b = { counts: new Map(), watchers: new Set(), snapshot: new Set() }; busyOf.set(client, b) }
  return b
}
function publish(b: Busy) { b.snapshot = new Set(b.counts.keys()); b.watchers.forEach(w => w()) }
/** Marks `ids` busy until the returned function is called. */
function markBusy(b: Busy, ids: number[]): () => void {
  ids.forEach(id => b.counts.set(id, (b.counts.get(id) ?? 0) + 1))
  publish(b)
  let released = false
  return () => {
    if (released) return
    released = true
    ids.forEach(id => { const n = (b.counts.get(id) ?? 1) - 1; if (n <= 0) b.counts.delete(id); else b.counts.set(id, n) })
    publish(b)
  }
}
/** Whether an upgrade or batch action started anywhere (the list or the server's page) is still running on this server. */
export function useServerBusy(id: number): boolean {
  return useBusyServers().has(id)
}

/** Every server with an upgrade or batch action in flight, for a list that gates each row's menu. */
export function useBusyServers(): ReadonlySet<number> {
  const b = busyFor(useQueryClient())
  const subscribe = (w: () => void) => { b.watchers.add(w); return () => { b.watchers.delete(w) } }
  return useSyncExternalStore(subscribe, () => b.snapshot, () => b.snapshot)
}

/**
 * The remote upgrades and the bulk actions of the list. Every one states what it will do before it does it (the
 * confirm names the server or the count) and puts focus on Cancel, because each restarts something on a machine the
 * panel does not own.
 */
export function useServerUpgrades({ probeServer, refresh, clearSelection }: {
  probeServer: (s: Server) => Promise<void>
  refresh: () => void
  clearSelection: () => void
}) {
  const { t } = useTranslation('admin')
  const busy = busyFor(useQueryClient())
  // The server whose upgrade request is in flight: set BEFORE the confirm opens, so a double click on the menu item
  // cannot stack two confirms (it once fired two upgrade requests).
  const [upgrading, setUpgrading] = useState<number | null>(null)
  const [batchBusy, setBatchBusy] = useState<'upgrade_agent' | 'upgrade_xray' | 'upgrade_panel' | 'delete' | ''>('')

  async function runUpgradePanel(s: Server, force = false): Promise<void> {
    setUpgrading(s.id)
    const release = markBusy(busy, [s.id])
    try {
      // ASK THE INSTANCE FIRST, when it can answer. The decision is the server's; a dialog that decided for itself
      // would be a second decision source, and the one the operator saw would not be the one enforced. A failure
      // here is NOT a refusal — the write path still protects the fire — so it falls through to the existing flow.
      const option = await upgradeOptions(s.id, 'panel').catch(() => null)
      if (option?.state === 'unsupported' || option?.state === 'blocked') {
        pushSnack(t('admin:servers.toast.upgrade_unavailable', { reasons: option.reason_codes.join(', ') }), 'warning')
        return
      }
      if (!force) {
        // Read-only pre-flight: target version, tested-range check and advisory, so the confirm warns about breaking
        // changes (especially ones that also restart the bundled Xray) BEFORE anything fires. If the preview itself
        // fails (server unreachable) the confirm is generic — the upgrade call's own gate still protects the fire.
        let preview: UpgradePreviewResult | null = null
        try { preview = await upgradePreview(s.id) } catch { preview = null }
        if (preview?.already_latest) {
          pushSnack(t('admin:servers.toast.upgrade_panel_already_latest', { version: preview.current_version ?? '' }), 'success')
          return
        }
        const advisory = preview?.advisory
        const lines = [t('admin:servers.confirm.upgrade_panel_message', { name: s.name })]
        if (preview?.target_version) lines.push(t('admin:servers.confirm.upgrade_target', { target: preview.target_version }))
        // The response says the target cannot be pinned, so the dialog must not present it as a version the panel
        // will be held to: leaving this out would make the confirm read as a promise the API explicitly denies.
        if (preview?.target_pinnable === false) lines.push(t('admin:servers.confirm.upgrade_target_unpinnable'))
        if (advisory?.text) lines.push(advisory.text)
        if (advisory?.affects_xray) lines.push(t('admin:servers.confirm.upgrade_affects_xray'))
        const ok = await confirm({
          title: t('admin:servers.confirm.upgrade_panel_title'),
          message: lines.join('\n\n'),
          confirmText: t('admin:servers.action.upgrade'),
          // A warning advisory (or one flagged for Xray) makes the confirm destructive-styled so the admin slows down.
          destructive: advisory?.severity === 'warning' || advisory?.affects_xray,
          initialFocus: 'cancel',
        })
        if (!ok) return
      }
      const r = await upgradePanel(s.id, { force })
      if (r.already_latest) {
        pushSnack(t('admin:servers.toast.upgrade_panel_already_latest', { version: r.current_version ?? '' }), 'success')
      } else {
        pushSnack(t('admin:servers.toast.upgrade_panel_started', { target: r.target_version ?? '?' }), 'success')
      }
    } catch (err) {
      const resp = (err as { response?: { status?: number; data?: {
        reason?: string; latest_version?: string; compat_status?: string; psp_max_xui?: string; can_force?: boolean; error?: string; message?: string
      } } }).response
      const body = resp?.data
      // 409 + reason "untested_target": the admin can override, after a second confirmation that spells out the risk.
      // The first call lets the admin see WHY it was blocked; the second is the explicit acceptance.
      if (resp?.status === 409 && body?.reason === 'untested_target' && body?.can_force && !force) {
        const proceed = await confirm({
          title: t('admin:servers.confirm.upgrade_force_title'),
          message: t('admin:servers.confirm.upgrade_force_message', {
            latest: body.latest_version ?? '?',
            max: body.psp_max_xui || t('admin:servers.compat.not_loaded'),
            status: body.compat_status ?? 'unknown',
          }),
          destructive: true,
          confirmText: t('admin:servers.action.force_upgrade'),
          initialFocus: 'cancel',
        })
        // Resend with force=true; the recursive call keeps the success and error branches shared.
        if (proceed) return runUpgradePanel(s, true)
        return
      }
      pushSnack(body?.error ?? body?.message ?? (err as { message?: string }).message ?? 'unknown', 'error')
    } finally {
      release()
      setUpgrading(null)
    }
  }

  // Passwall Node agents, each to the newest release its update channel offers (what the Version column calls "Update
  // available"). The node checks the expected version and the upgrade path itself and refuses what it cannot walk, so a
  // refusal here is reported per server, never retried or forced. Pinning another version stays in the per-server dialog.
  async function batchUpgradeAgent(wanted: { server: Server; target: string }[]) {
    if (!wanted.length) return
    // The same check the per-server dialog makes first: a node that refuses (an uncanonical version, no updater) or that
    // does not offer this target is left out and named, rather than sent a request the operator then sees refused.
    const checked = await allSettledLimited(wanted, r => upgradeOptions(r.server.id, 'agent'))
    const rows: typeof wanted = []
    const skipped: string[] = []
    wanted.forEach((r, i) => {
      const res = checked[i]
      // Best-effort, as in the dialog: an unanswered check leaves the decision to the node.
      if (res.status !== 'fulfilled') { rows.push(r); return }
      const o = res.value
      const offered = o.targets?.map(x => x.version)
      if (o.state === 'blocked' || (offered && offered.length > 0 && !offered.includes(r.target))) skipped.push(r.server.name)
      else rows.push(r)
    })
    if (!rows.length) {
      pushSnack(t('admin:servers.toast.batch_upgrade_agent_none', { names: skipped.join(', ') }), 'warning')
      return
    }
    const ok = await confirm({
      title: t('admin:servers.confirm.batch_upgrade_agent_title'),
      message: [t('admin:servers.confirm.batch_upgrade_agent_message', { count: rows.length }),
        ...rows.map(r => `${r.server.name}: ${r.server.panel_version?.split(' ')[0] ?? '?'} → ${r.target}`),
        ...(skipped.length ? ['', t('admin:servers.confirm.batch_upgrade_agent_skipped', { names: skipped.join(', ') })] : [])].join('\n'),
      confirmText: t('admin:servers.action.upgrade'),
      destructive: true,
      initialFocus: 'cancel',
    })
    if (!ok) return
    setBatchBusy('upgrade_agent')
    const release = markBusy(busy, rows.map(r => r.server.id))
    try {
      const results = await allSettledLimited(rows, r =>
        requestNativeAgentUpgrade(r.server.id, r.target, r.server.panel_version?.split(' ')[0] ?? '', crypto.randomUUID()))
      const success = results.filter(r => r.status === 'fulfilled').length
      const failed = rows.length - success
      if (failed > 0) {
        const names = rows.filter((_, i) => results[i].status !== 'fulfilled').map(r => r.server.name).join(', ')
        pushSnack(t('admin:servers.toast.batch_upgrade_agent_partial', { ok: success, fail: failed, names }), 'warning')
      } else pushSnack(t('admin:servers.toast.batch_upgrade_agent_ok', { count: success }), 'success')
      clearSelection()
      refresh()
    } finally { release(); setBatchBusy('') }
  }

  // Latest-only by design: pinning a version stays in the single-server dialog, because different servers offer
  // different lists and "v25.10.31 on all" cannot always be satisfied. Each 3X-UI resolves "latest" itself.
  async function batchUpgradeXray(rows: Server[]) {
    if (!rows.length) return
    const ok = await confirm({
      title: t('admin:servers.confirm.batch_upgrade_xray_title'),
      message: t('admin:servers.confirm.batch_upgrade_xray_message', { count: rows.length }),
      confirmText: t('admin:servers.action.upgrade'),
      initialFocus: 'cancel',
    })
    if (!ok) return
    setBatchBusy('upgrade_xray')
    const release = markBusy(busy, rows.map(r => r.id))
    try {
      const results = await allSettledLimited(rows, r => upgradeXray(r.id))
      const success = results.filter(r => r.status === 'fulfilled').length
      const failed = rows.length - success
      // Refresh the version snapshot of the ones that succeeded, so the Version column shows the new core.
      void allSettledLimited(rows.filter((_, i) => results[i].status === 'fulfilled'), s => probeServer(s))
      if (failed > 0) pushSnack(t('admin:servers.toast.batch_upgrade_xray_partial', { ok: success, fail: failed }), 'warning')
      else pushSnack(t('admin:servers.toast.batch_upgrade_xray_ok', { count: success }), 'success')
    } finally { release(); setBatchBusy('') }
  }

  // UNLIKE the core, a panel upgrade restarts the whole panel process — every user on it drops for 30-60 seconds — and
  // a new release can break the management API. So this batch path deliberately does NOT force: the compatibility gate
  // still blocks any server whose target is outside the tested range, and those are reported as blocked rather than
  // overridden. Forcing stays a per-server decision, where the friction is appropriate.
  async function batchUpgradePanel(rows: Server[]) {
    if (!rows.length) return
    const ok = await confirm({
      title: t('admin:servers.confirm.batch_upgrade_panel_title'),
      message: t('admin:servers.confirm.batch_upgrade_panel_message', { count: rows.length }),
      confirmText: t('admin:servers.action.upgrade'),
      destructive: true,
      initialFocus: 'cancel',
    })
    if (!ok) return
    setBatchBusy('upgrade_panel')
    const release = markBusy(busy, rows.map(r => r.id))
    try {
      const results = await allSettledLimited(rows, r => upgradePanel(r.id))
      let initiated = 0; let alreadyLatest = 0; let blocked = 0; let failed = 0
      results.forEach(res => {
        if (res.status === 'fulfilled') { if (res.value?.already_latest) alreadyLatest++; else initiated++; return }
        const resp = (res.reason as { response?: { status?: number; data?: { reason?: string } } }).response
        if (resp?.status === 409 && resp.data?.reason === 'untested_target') blocked++
        else failed++
      })
      if (blocked === 0 && failed === 0) {
        pushSnack(alreadyLatest > 0
          ? t('admin:servers.toast.batch_upgrade_panel_ok_latest', { count: initiated, latest: alreadyLatest })
          : t('admin:servers.toast.batch_upgrade_panel_ok', { count: initiated }), 'success')
      } else {
        pushSnack(t('admin:servers.toast.batch_upgrade_panel_partial', { ok: initiated, latest: alreadyLatest, blocked, fail: failed }), 'warning')
      }
      // A disruptive batch clears the selection when it completes, so an immediate re-click cannot fire it again
      // without fresh intent.
      clearSelection()
    } finally { release(); setBatchBusy('') }
  }

  async function batchDelete(rows: Server[]) {
    if (!rows.length) return
    const names = rows.slice(0, 5).map(r => r.name).join('、')
    const suffix = rows.length > 5 ? ` +${rows.length - 5}` : ''
    const ok = await confirm({
      title: t('admin:servers.confirm.batch_delete_title'),
      message: t('admin:servers.confirm.batch_delete_message', { names, suffix }),
      destructive: true,
      confirmText: t('admin:servers.action.delete'),
      initialFocus: 'cancel',
    })
    if (!ok) return
    setBatchBusy('delete')
    const release = markBusy(busy, rows.map(r => r.id))
    try {
      const results = await allSettledLimited(rows, r => deleteServer(r.id))
      const okCount = results.filter(r => r.status === 'fulfilled').length
      const failed = rows.length - okCount
      clearSelection()
      if (failed > 0) pushSnack(t('admin:servers.toast.batch_partial', { ok: okCount, fail: failed }), 'warning')
      else pushSnack(t('admin:servers.toast.batch_deleted', { count: okCount }), 'success')
      refresh()
    } finally { release(); setBatchBusy('') }
  }

  return { upgrading, batchBusy, runUpgradePanel, batchUpgradeAgent, batchUpgradeXray, batchUpgradePanel, batchDelete }
}
