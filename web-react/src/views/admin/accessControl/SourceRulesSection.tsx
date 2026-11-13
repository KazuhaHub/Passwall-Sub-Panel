import { useEffect, useMemo, useState, type MutableRefObject } from 'react'
import { Alert, Autocomplete, Box, Button, FormControlLabel, Skeleton, Switch, TextField, Typography, useTheme } from '@mui/material'
import { useTranslation } from 'react-i18next'

import { destErrorOf, isDestUnavailable } from '@/api/accessControl'
import { AsyncButton } from '@/components/AsyncButton'
import { confirm } from '@/components/ConfirmHost'
import FieldHint from '@/components/FieldHint'
import { pushSnack } from '@/components/SnackbarHost'
import { useDestPolicies, useDestSourceRule, useDestSourceRules, useDestStatus, usePutSourceRule } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { applyMinutes, enforcingNodeCount, needsFirstPublishConfirm } from '@/utils/accessControl'
import { firstPublishConfirm } from './confirmCopy'
import { destErrorText } from './destErrors'

// ISO 3166-1 alpha-2 officially assigned codes: the choices of "deny source
// country". Names are localised with Intl.DisplayNames at render time.
export const ISO_COUNTRY_CODES: readonly string[] = (
  'AD AE AF AG AI AL AM AO AQ AR AS AT AU AW AX AZ BA BB BD BE BF BG BH BI BJ BL BM BN BO BQ BR BS BT BV BW BY BZ ' +
  'CA CC CD CF CG CH CI CK CL CM CN CO CR CU CV CW CX CY CZ DE DJ DK DM DO DZ EC EE EG EH ER ES ET FI FJ FK FM FO FR ' +
  'GA GB GD GE GF GG GH GI GL GM GN GP GQ GR GS GT GU GW GY HK HM HN HR HT HU ID IE IL IM IN IO IQ IR IS IT JE JM JO ' +
  'JP KE KG KH KI KM KN KP KR KW KY KZ LA LB LC LI LK LR LS LT LU LV LY MA MC MD ME MF MG MH MK ML MM MN MO MP MQ MR ' +
  'MS MT MU MV MW MX MY MZ NA NC NE NF NG NI NL NO NP NR NU NZ OM PA PE PF PG PH PK PL PM PN PR PS PT PW PY QA RE RO ' +
  'RS RU RW SA SB SC SD SE SG SH SI SJ SK SL SM SN SO SR SS ST SV SX SY SZ TC TD TF TG TH TJ TK TL TM TN TO TR TT TV ' +
  'TW TZ UA UG US UM UY UZ VA VC VE VG VI VN VU WF WS YE YT ZA ZM ZW'
).split(' ')

export function countryName(code: string, lang: string): string {
  try {
    return new Intl.DisplayNames([lang], { type: 'region' }).of(code) ?? code
  } catch {
    return code
  }
}

const V4 = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})\/(\d{1,2})$/
/** A CIDR prefix, IPv4 or IPv6 (the server canonicalises it; this only catches typos). */
export function isCidr(line: string): boolean {
  const v4 = V4.exec(line)
  if (v4) return v4.slice(1, 5).every(o => Number(o) <= 255) && Number(v4[5]) <= 32
  const m = /^([0-9a-fA-F:.]+)\/(\d{1,3})$/.exec(line)
  if (!m || !m[1].includes(':') || Number(m[2]) > 128) return false
  return /^[0-9a-fA-F:]+$/.test(m[1]) && (m[1].match(/::/g) ?? []).length <= 1 && m[1].split(':').length <= 8
}

/** The non-empty lines, trimmed, and the 1-based numbers of those that are not CIDRs. */
export function parseCidrLines(text: string): { cidrs: string[]; bad: number[] } {
  const cidrs: string[] = []
  const bad: number[] = []
  text.split('\n').forEach((raw, i) => {
    const line = raw.trim()
    if (!line) return
    if (isCidr(line)) cidrs.push(line)
    else bad.push(i + 1)
  })
  return { cidrs, bad }
}

const S = 'admin:access_control.source_rules.'

/**
 * "Deny source country or region" for one inbound of a native node. It is a
 * different endpoint from the node's own save (PUT /dest/source-rules/:id), so
 * it keeps its own [Save], its own pending state and its own restart sentence;
 * it is never folded into the node dialog's OK.
 */
/** What the host dialog asks of the draft: is it unsaved, and save it now (true = nothing left unsaved). */
export interface SourceDraftHandle {
  dirty: () => boolean
  save: () => Promise<boolean>
}

export default function SourceRulesSection({ nodeId, dirtyRef }: {
  nodeId: number
  /** Filled with the draft's handle; the host asks it before closing or saving the inbound. */
  dirtyRef?: MutableRefObject<SourceDraftHandle | null>
}) {
  const { t, i18n } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const scope = useQueryScope()
  const read = useDestSourceRule(scope, nodeId)
  const all = useDestSourceRules(scope)
  const policies = useDestPolicies(scope)
  const status = useDestStatus(scope)
  const put = usePutSourceRule(scope)

  const [countries, setCountries] = useState<string[]>([])
  const [cidrText, setCidrText] = useState('')
  const [on, setOn] = useState(false)
  // A dialog that is gone has no draft to guard.
  useEffect(() => () => { if (dirtyRef) dirtyRef.current = null }, [dirtyRef])
  const [seeded, setSeeded] = useState<string>('')
  const [fieldError, setFieldError] = useState<{ field?: string; text: string } | null>(null)

  const rule = read.data?.rule
  // Seed the draft once per read of the rule (its updated_at): a refetch of
  // the same rule never clobbers what the admin is typing.
  const seedKey = rule ? `${nodeId}:${rule.updated_at ?? 0}:${rule.state}` : ''
  useEffect(() => {
    if (!rule || seedKey === seeded) return
    setSeeded(seedKey)
    setCountries(rule.countries)
    setCidrText(rule.cidrs.join('\n'))
    setOn(rule.state === 'on')
    setFieldError(null)
  }, [rule, seedKey, seeded])

  const options = useMemo(
    () => [...ISO_COUNTRY_CODES].sort((a, b) => countryName(a, i18n.language).localeCompare(countryName(b, i18n.language), i18n.language)),
    [i18n.language],
  )
  const parsed = parseCidrLines(cidrText)

  if (read.isPending) return <Skeleton variant="rounded" height={120} sx={{ mt: 2 }} />
  if (read.isError || !read.data) {
    if (isDestUnavailable(read.error)) return null
    return (
      <Alert severity="error" sx={{ mt: 2 }} action={<Button color="inherit" size="small" onClick={() => void read.refetch()}>{t('admin:access_control.retry')}</Button>}>
        {t(`${S}read_failed`)}
      </Alert>
    )
  }
  if (!read.data.native) return null

  const dirty = !!rule && (
    on !== (rule.state === 'on')
    || countries.join() !== rule.countries.join()
    || parsed.cidrs.join() !== rule.cidrs.join()
  )
  const minutes = applyMinutes(status.data)
  const geoip = read.data.geoip_available

  const save = async (): Promise<boolean> => {
    if (!rule) return false
    if (parsed.bad.length > 0) {
      setFieldError({ field: 'cidrs', text: t(`${S}bad_lines`, { lines: parsed.bad.join(', ') }) })
      return false
    }
    const policiesOn = [...(policies.data?.allow ?? []), ...(policies.data?.block ?? [])].filter(p => p.state === 'on').length
    const sourceRulesOn = (all.data?.items ?? []).filter(r => r.state === 'on' && r.node_id !== nodeId).length
    if (needsFirstPublishConfirm({ policiesOn, sourceRulesOn }, { turnsOn: on && (countries.length > 0 || parsed.cidrs.length > 0) })) {
      const nodes = enforcingNodeCount(status.data)
      const ok = await confirm(firstPublishConfirm({
        t, minutes, nodes, seconds: null,
      }))
      if (!ok) return false
    }
    setFieldError(null)
    try {
      await put.mutateAsync({
        nodeId,
        input: { countries, cidrs: parsed.cidrs, state: on ? 'on' : 'off', updated_at: rule.updated_at ?? undefined },
      })
      pushSnack(t(`${S}saved`), 'success')
      return true
    } catch (err) {
      const e = destErrorOf(err)
      if (e?.code === 'dest_source_rule_stale') {
        setFieldError({ text: t(`${S}stale`) })
        void read.refetch()
      } else if (e?.status === 400 && e.field) {
        setFieldError({ field: e.field, text: destErrorText(t, err) })
      } else {
        setFieldError({ text: destErrorText(t, err) })
      }
      return false
    }
  }
  if (dirtyRef) dirtyRef.current = { dirty: () => dirty, save }

  return (
    <Box data-testid="source-rules-section" sx={{ mt: 2.5, p: 2, borderRadius: 3, bgcolor: md.surfaceContainerLow, border: `1px solid ${md.outlineVariant}` }}>
      <Typography component="h2" sx={{ fontSize: 16, fontWeight: 600 }}>{t(`${S}title`)}</Typography>
      <Typography variant="body2" sx={{ color: md.onSurfaceVariant, mb: 1.5 }}>{t(`${S}hint`)}</Typography>
      {!geoip && (
        <Box sx={{ mb: 1.5 }}>
          <FieldHint tone="amber" summary={t(`${S}geoip_missing.summary`)} detail={t(`${S}geoip_missing.detail`)} />
        </Box>
      )}
      {rule?.note === 'geoip_unavailable' && geoip && (
        <Box sx={{ mb: 1.5 }}>
          <FieldHint tone="amber" summary={t(`${S}note.geoip_unavailable.summary`)} detail={t(`${S}note.geoip_unavailable.detail`)} />
        </Box>
      )}
      {rule?.note === 'empty' && (
        <Box sx={{ mb: 1.5 }}>
          <FieldHint tone="muted" summary={t(`${S}note.empty.summary`)} detail={t(`${S}note.empty.detail`)} />
        </Box>
      )}
      {fieldError && !fieldError.field && <Alert severity="error" sx={{ mb: 1.5 }}>{fieldError.text}</Alert>}
      <Autocomplete multiple size="small" options={options} value={countries} disabled={put.isPending}
        onChange={(_, v) => setCountries(v)} filterSelectedOptions
        getOptionLabel={c => `${countryName(c, i18n.language)} (${c})`}
        renderInput={params => (
          <TextField {...params} label={t(`${S}countries`)} error={fieldError?.field === 'countries'}
            helperText={fieldError?.field === 'countries' ? fieldError.text : undefined} />
        )} />
      <TextField multiline minRows={3} fullWidth size="small" sx={{ mt: 2 }} value={cidrText} disabled={put.isPending}
        label={t(`${S}cidrs`)} placeholder="203.0.113.0/24" onChange={e => setCidrText(e.target.value)}
        error={parsed.bad.length > 0 || fieldError?.field === 'cidrs'}
        helperText={parsed.bad.length > 0 ? t(`${S}bad_lines`, { lines: parsed.bad.join(', ') }) : fieldError?.field === 'cidrs' ? fieldError.text : t(`${S}cidrs_hint`)}
        slotProps={{ htmlInput: { style: { fontFamily: 'ui-monospace, monospace', fontSize: 13 } } }} />
      {dirty && (
        <Box sx={{ mt: 1 }}>
          <FieldHint tone="amber" summary={t(`${S}unsaved.summary`)} detail={t(`${S}unsaved.detail`)} />
        </Box>
      )}
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 2, mt: 1.5, flexWrap: 'wrap' }}>
        <FormControlLabel label={t(`${S}enabled`)} control={<Switch checked={on} disabled={put.isPending} onChange={(_, c) => setOn(c)} />} />
        <Box sx={{ flex: 1 }} />
        <AsyncButton variant="contained" size="small" disabled={!dirty} pending={put.isPending} onClick={save}>{t(`${S}save`)}</AsyncButton>
      </Box>
      <Typography variant="caption" sx={{ display: 'block', mt: 0.5, color: md.onSurfaceVariant }}>
        {minutes === null ? t(`${S}restart_unknown`) : t(`${S}restart`, { count: minutes })}
      </Typography>
    </Box>
  )
}
