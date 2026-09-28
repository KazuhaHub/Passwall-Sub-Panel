import { useState } from 'react'
import { Link as RouterLink, useSearchParams } from 'react-router'
import { Alert, Box, Button, Paper, Skeleton, Typography, useTheme } from '@mui/material'
import { useTranslation } from 'react-i18next'
import { isAxiosError } from 'axios'

import type { UISettings } from '@/api/settings'
import { pushSnack } from '@/components/SnackbarHost'
import { useAllGroups } from '@/query/groups'
import { useRiskPolicy, useSaveRiskPolicy } from '@/query/riskCenter'
import { useGeoIPStatus } from '@/query/settings'
import { useQueryScope } from '@/query/useQueryScope'
import { activeDbIsCountryOnly } from '@/utils/geoAnomaly'
import { listSeparator } from '@/utils/riskCenter'
import { parsePolicyGroup, policyGroupSearch } from '../riskParams'
import DetectorCard from './DetectorCard'
import { effectiveGeo, effectiveRisk } from './effective'
import GroupExceptionsCard, { useGroupExceptions } from './GroupExceptionsCard'
import {
  changedKeys, pick, POLICY_KEYS, type RiskPolicyKey, type RiskPolicySettings, type RiskPolicyView,
} from './policyKeys'
import { advancedConfigured, cardFields, outOfRange, POLICY_CARDS, type PolicyCardId } from './policyLayout'
import { useLeaveGuard } from './useLeaveGuard'

const P = 'admin:risk_center.policy.'

type GeoScope = UISettings['geo_anomaly_scope']

/** What the failed read or save says: the server's words, else the transport's. */
function errorText(err: unknown): string {
  if (isAxiosError(err)) return String((err.response?.data as { error?: string } | undefined)?.error ?? err.message)
  return err instanceof Error ? err.message : String(err)
}

/** The granularity to restore when the geo card is switched back on. */
function scopeMemory(s: RiskPolicySettings): GeoScope {
  return s.geo_anomaly_scope && s.geo_anomaly_scope !== 'off' ? s.geo_anomaly_scope : ''
}

/**
 * 策略: every concurrent-location and risk-signal setting, one card per
 * detector, and one group's exceptions under them (§3.8). The risk center
 * owns these keys (GET/PUT /risk-center/policy); the page shows the served
 * default in every empty field and keeps no copy of any.
 */
export default function PolicyTab() {
  const { t } = useTranslation(['admin'])
  const scope = useQueryScope()
  const q = useRiskPolicy(scope)

  if (q.data) return <PolicyBody loaded={q.data} />
  if (q.error) {
    return isAxiosError(q.error) && q.error.response?.status === 503
      ? <Alert severity="info" sx={{ fontSize: 13 }}>{t('admin:risk_center.unwired')}</Alert>
      : (
        <Alert severity="error" sx={{ fontSize: 13 }}
          action={(
            <Button size="small" color="inherit" onClick={() => void q.refetch()}>{t('admin:risk_center.retry')}</Button>
          )}>
          {t('admin:risk_center.load_failed', { error: errorText(q.error) })}
        </Alert>
      )
  }
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
      {[0, 1, 2].map(i => <Skeleton key={i} variant="rounded" height={160} />)}
    </Box>
  )
}

/**
 * The page once the policy is read. It keeps TWO copies of the policy:
 * `seed`, what the draft started from, and `draft`, what the fields show.
 * Both are seeded once, from the first read; after that only the admin's
 * own actions reseed them — a save (from its answer), 放弃修改 and 载入最新.
 * So a refetch on focus never rewrites what the admin is typing, and
 * "unsaved" is draft against seed, never against the live read.
 *
 * The live read is still watched: when it differs from the seed on a key
 * the admin did NOT change, another admin saved since, and the page says so
 * and offers to rebase — the admin's edits on top of the newer policy.
 */
function PolicyBody({ loaded }: { loaded: RiskPolicyView }) {
  const { t, i18n } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const scope = useQueryScope()
  const save = useSaveRiskPolicy(scope)
  const { data: geoip } = useGeoIPStatus(scope)
  const groupsQ = useAllGroups(scope)
  const [params, setParams] = useSearchParams()
  const groupId = parsePolicyGroup(params)
  const exceptions = useGroupExceptions(groupId)

  const [seed, setSeed] = useState<RiskPolicySettings>(() => loaded.settings)
  const [draft, setDraft] = useState<RiskPolicySettings>(() => loaded.settings)
  // The geo card's switch is its scope: off stores 'off', and switching on
  // again restores the granularity it had, which is remembered here.
  const [lastScope, setLastScope] = useState<GeoScope>(() => scopeMemory(loaded.settings))
  // Each card's 高级 panel: open on arrival when something in it is set, so
  // a changed value never hides behind a closed panel.
  const [open, setOpen] = useState<Record<string, boolean>>(
    () => Object.fromEntries(POLICY_CARDS.map(c => [c.id, advancedConfigured(c, loaded.settings)])),
  )
  // What the server refused, by key (a bad ignore list).
  const [errors, setErrors] = useState<Partial<Record<RiskPolicyKey, string>>>({})

  const { defaults, effective } = loaded
  const changed = changedKeys(draft, seed)
  const dirty = changed.length > 0
  const stale = POLICY_KEYS.some(k => !changed.includes(k) && loaded.settings[k] !== seed[k])
  const invalid = POLICY_CARDS.flatMap(cardFields).filter(f => outOfRange(f, draft[f.key])).length
  const groups = groupsQ.data
  const groupName = groups?.find(g => g.id === groupId)?.name ?? `#${groupId}`

  useLeaveGuard(dirty || exceptions.dirty, {
    title: t(`${P}leave_title`), message: t(`${P}leave_message`), confirmText: t(`${P}leave_confirm`),
    destructive: true,
  })

  const patch = (p: Partial<RiskPolicySettings>) => {
    setDraft(d => ({ ...d, ...p }))
    // Editing a field the server refused is fixing it: its mark goes.
    setErrors(e => (Object.keys(p).some(k => k in e)
      ? Object.fromEntries(Object.entries(e).filter(([k]) => !(k in p)))
      : e))
  }

  const reseed = (next: RiskPolicySettings) => {
    setSeed(next)
    setDraft(next)
    setLastScope(scopeMemory(next))
    setErrors({})
  }

  const discard = () => {
    reseed(loaded.settings)
    exceptions.discard()
  }

  // Rebase: the newer policy, with this admin's edits on top of it.
  const loadLatest = () => {
    setSeed(loaded.settings)
    setDraft({ ...loaded.settings, ...pick(draft, changed) })
    if (!changed.includes('geo_anomaly_scope')) setLastScope(scopeMemory(loaded.settings))
  }

  const onSave = () => {
    setErrors({})
    save.mutate(pick(draft, changed), {
      onSuccess: view => {
        reseed(view.settings)
        pushSnack(t(`${P}saved`), 'success')
        // The group rows show the global value they inherit.
        void exceptions.reloadBaseline()
      },
      onError: err => {
        const body = isAxiosError(err) ? err.response?.data as { error?: string; field?: string; bad?: string[] } : undefined
        const key = body?.field as RiskPolicyKey | undefined
        const card = key ? POLICY_CARDS.find(c => cardFields(c).some(f => f.key === key)) : undefined
        if (!key || !card) {
          pushSnack(t(`${P}save_failed`, { error: errorText(err) }), 'error')
          return
        }
        // The field the server named: marked with what it named, its panel
        // opened, and scrolled to once the panel has rendered it.
        setErrors({ [key]: body?.bad?.length ? body.bad.join(', ') : (body?.error ?? '') })
        if (card.advanced?.some(f => f.key === key)) setOpen(o => ({ ...o, [card.id]: true }))
        setTimeout(() => {
          document.querySelector(`[data-policy-key="${key}"]`)?.scrollIntoView?.({ block: 'center' })
        }, 0)
      },
    })
  }

  // The geo card while switched off shows the granularity it will come back
  // with, and a pick there is remembered rather than switching it on.
  const geoOff = draft.geo_anomaly_scope === 'off'
  const geoDraft = geoOff ? { ...draft, geo_anomaly_scope: lastScope } : draft
  const patchGeo = (p: Partial<RiskPolicySettings>) => {
    if (geoOff && 'geo_anomaly_scope' in p) {
      setLastScope(p.geo_anomaly_scope ?? '')
      return
    }
    patch(p)
  }
  const toggleGeo = (on: boolean) => {
    if (on) {
      patch({ geo_anomaly_scope: lastScope })
    } else {
      setLastScope(scopeMemory(draft))
      patch({ geo_anomaly_scope: 'off' })
    }
  }

  // What the server will judge with. The lines need the served defaults; a
  // server that sent none gets no line rather than a guess.
  const geoEff = defaults.geo_anomaly_max_places !== undefined ? effectiveGeo(draft, defaults) : null
  // The line names all three tiers, which only the city scope (the default,
  // '') judges; under region or country it would promise a line never drawn.
  const tiered = (geoDraft.geo_anomaly_scope || 'city') === 'city'
  const riskEff = defaults.risk_min_days !== undefined
    ? effectiveRisk(draft, defaults, effective.risk_window_days)
    : null
  const geoipState = !geoip ? null
    : !geoip.enabled || !geoip.active ? 'geoip_off'
      : activeDbIsCountryOnly(geoip) ? 'geoip_country' : 'geoip_city'

  const caption = (text: string) => (
    <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>{text}</Typography>
  )

  const cardLines = (id: PolicyCardId) => {
    switch (id) {
      case 'geo':
        return (
          <>
            {geoEff && tiered && caption(t('admin:settings.geo_anomaly.effective', {
              countries: geoEff.flag.countries + 1, regions: geoEff.flag.regions + 1, cities: geoEff.flag.cities + 1,
            }))}
            {/* P11: without the database every verdict is "cannot tell", and
                a quiet page would read as a clean fleet. */}
            {geoipState && (
              <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap' }}>
                <Typography sx={{ fontSize: 12, color: geoipState === 'geoip_off' ? md.error : md.onSurfaceVariant }}>
                  {t(`${P}geoip_status`, { state: t(`${P}${geoipState}`) })}
                </Typography>
                <Button component={RouterLink} to="/admin/settings?tab=general" size="small">
                  {t(`${P}geoip_open`)}
                </Button>
              </Box>
            )}
          </>
        )
      case 'sub_spread':
        return caption(t('admin:settings.risk.sub_spread_hint'))
      case 'devices':
        return caption(t(`${P}hwid_on_devices`))
      default:
        return null
    }
  }

  const toggleOf = (id: PolicyCardId): { on?: boolean; onToggle?: (on: boolean) => void } => {
    const card = POLICY_CARDS.find(c => c.id === id)
    if (card?.toggle?.kind === 'geo_scope') return { on: !geoOff, onToggle: toggleGeo }
    if (card?.toggle?.kind === 'inverted') {
      const key = card.toggle.key
      return { on: draft[key] !== true, onToggle: on => patch({ [key]: !on } as Partial<RiskPolicySettings>) }
    }
    return {}
  }

  const parts = [
    dirty && t(`${P}part_policy`),
    exceptions.dirty && t(`${P}part_group`, { name: groupName }),
  ].filter((x): x is string => !!x)

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
      {stale && (
        <Alert severity="info" sx={{ fontSize: 13 }}
          action={<Button size="small" color="inherit" onClick={loadLatest}>{t(`${P}load_latest`)}</Button>}>
          {t(`${P}stale`)}
        </Alert>
      )}

      {POLICY_CARDS.map(card => (
        <DetectorCard key={card.id} card={card} draft={card.id === 'geo' ? geoDraft : draft}
          defaults={defaults} effective={effective} errors={errors}
          {...toggleOf(card.id)}
          onPatch={card.id === 'geo' ? patchGeo : patch}
          advancedOpen={!!open[card.id]} onAdvancedChange={o => setOpen(prev => ({ ...prev, [card.id]: o }))}
          descValues={card.id === 'devices' ? { days: riskEff?.minDays ?? '—' } : undefined}
          lines={cardLines(card.id)}
          disposalLines={card.id === 'geo' && geoEff && tiered ? caption(t('admin:settings.geo_anomaly.ban_effective', {
            countries: geoEff.ban.countries + 1, regions: geoEff.ban.regions + 1, cities: geoEff.ban.cities + 1,
            polls: geoEff.banAfterPolls, minutes: geoEff.banMinutes,
          })) : undefined} />
      ))}

      <GroupExceptionsCard groups={groups} groupId={groupId} exceptions={exceptions}
        onPickGroup={id => setParams(prev => policyGroupSearch(prev, id), { replace: true })} />

      {/* Sticky at the bottom, so what is unsaved and the save are in reach
          from any card. The group card saves on its own (D7); its part is
          named here so the page never looks clean while it is not. */}
      <Paper elevation={3} sx={{
        position: 'sticky', bottom: 0, zIndex: 2, px: 2, py: 1.25, borderRadius: 3,
        display: 'flex', alignItems: 'center', gap: 1.5, flexWrap: 'wrap', bgcolor: md.surfaceContainerHigh,
      }}>
        <Box sx={{ flex: 1, minWidth: 0 }}>
          {parts.length > 0 && (
            <Typography sx={{ fontSize: 13 }}>{t(`${P}dirty_parts`, { parts: parts.join(listSeparator(i18n.language)) })}</Typography>
          )}
          {invalid > 0 && (
            <Typography sx={{ fontSize: 13, color: md.error }}>{t(`${P}invalid`, { count: invalid })}</Typography>
          )}
        </Box>
        <Button disabled={!dirty && !exceptions.dirty} onClick={discard}>{t(`${P}discard`)}</Button>
        <Button variant="contained" disabled={!dirty || invalid > 0 || save.isPending} onClick={onSave}>
          {t(`${P}save`)}
        </Button>
      </Paper>
    </Box>
  )
}
