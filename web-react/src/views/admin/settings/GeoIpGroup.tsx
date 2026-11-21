import { useEffect, useState } from 'react'
import { getGeoIPStatus, updateGeoIPNow, type GeoIPStatus, type UISettings } from '@/api/settings'
import { AsyncButton } from '@/components/common/AsyncButton'
import { pushSnack } from '@/components/feedback/toasts'
import { Field } from '@/components/form/Field'
import { SecretField } from '@/components/form/SecretField'
import { useAdminT } from '../useAdminT'
import type { KeysOf } from './model'
import { settingId } from './model'
import { Group, Hint, NumField, Pair, SelectRow, SwitchField, TextRow } from './parts'
import type { UiSection } from './useUiSection'

type Source = NonNullable<UISettings['geo_ip_update_source']>

/**
 * IP region display for the access logs: the offline database, where it comes from and how it is kept up to date.
 * The database list and the update progress are not part of the settings record; they come from the status
 * endpoint, which this polls while an update downloads.
 */
export function GeoIpGroup({ form }: { form: UiSection<KeysOf<'general'>> }) {
  const t = useAdminT()
  const { value: v, patch, saved } = form
  const [status, setStatus] = useState<GeoIPStatus | null>(null)
  const [busy, setBusy] = useState(false)

  useEffect(() => { void getGeoIPStatus().then(setStatus).catch(() => { /* not fatal: the section still renders */ }) }, [])

  // The download runs on the server, off the request (inline it made a reverse proxy answer 502 before the panel
  // could reply), so this starts it and polls until it finishes, refreshing the list and showing the real result.
  async function update() {
    setBusy(true)
    try {
      // The server reads the STORED token and source, not this form: save what is typed first. A save that did not
      // happen (a rule broken, a request failed) says why itself, so the update does not start.
      if (form.dirty && !(await form.save({ quiet: true }))) return
      await updateGeoIPNow()
      const deadline = Date.now() + 5 * 60 * 1000
      while (Date.now() < deadline) {
        await new Promise(r => setTimeout(r, 2000))
        const st = await getGeoIPStatus()
        setStatus(st)
        if (!st.update.updating) {
          if (st.update.last_error) pushSnack(st.update.last_error, 'warning')
          else pushSnack(t('settings.geo.update_ok') + (st.update.last_file || ''), 'success')
          return
        }
      }
      pushSnack(t('settings.geo.update_running'), 'info')
    } catch (e) {
      pushSnack((e as { response?: { data?: { error?: string } } })?.response?.data?.error || String(e), 'warning')
    } finally { setBusy(false) }
  }

  const source: Source = v.geo_ip_update_source || 'maxmind'
  const available = status?.available ?? []
  const tokenLabel = source === 'maxmind' ? t('settings.geo.token_maxmind') : source === 'ipinfo' ? t('settings.geo.token_ipinfo') : t('settings.geo.token_custom')
  const describe = (db: (typeof available)[number]) => `${db.file} — ${db.granularity}${db.type ? ` · ${db.type}` : ''}`

  return (
    <Group title={t('settings.geo.section')}>
      <SwitchField name="geo_ip_enabled" label={t('settings.geo.enabled')} checked={v.geo_ip_enabled} onChange={c => patch({ geo_ip_enabled: c })}
        hint={<>{t('settings.geo.hint')}<code className="rounded bg-muted px-1 py-0.5 text-[11px]">{status?.dir || '<ConfigDir>/geoip/'}</code>{t('settings.geo.hint2')}</>} />

      <SelectRow name="geo_ip_db_file" label={t('settings.geo.active_db')} hint={t('settings.geo.active_db_hint')} value={v.geo_ip_db_file ?? ''}
        onChange={f => patch({ geo_ip_db_file: f })}
        options={[{ value: '', label: t('settings.geo.auto') }, ...available.map(db => ({ value: db.file, label: describe(db) }))]} />

      <ul className="grid gap-0.5 text-xs text-muted-foreground" aria-label={t('settings.geo.active_db')}>
        {available.length === 0
          ? <li>{t('settings.geo.no_db')}</li>
          : available.map(db => (
            <li key={db.file} className="break-all">
              <span aria-hidden>{db.active ? '● ' : '○ '}</span>
              {db.file}
              {db.error
                ? ` — ${db.error}`
                : ` — ${db.granularity}${db.type ? ` · ${db.type}` : ''}${db.build_epoch ? ` · ${t('settings.geo.built')} ${new Date(db.build_epoch * 1000).toLocaleDateString()}` : ''}`}
            </li>
          ))}
      </ul>

      <SwitchField name="geo_ip_auto_update" label={t('settings.geo.auto_update')} checked={v.geo_ip_auto_update} onChange={c => patch({ geo_ip_auto_update: c })} />
      <div className="max-w-xs">
        {/* A stored 0 reads as the default 12 and is only replaced once the admin edits it, as before. */}
        <NumField name="geo_ip_update_interval_hours" label={t('settings.geo.update_interval_hours')} unit={t('settings.unit.hours')} min={1}
          hint={t('settings.geo.update_interval_hint')} disabled={!v.geo_ip_auto_update}
          value={v.geo_ip_update_interval_hours || 12} onChange={n => patch({ geo_ip_update_interval_hours: n })} />
      </div>

      <Pair>
        <SelectRow name="geo_ip_update_source" label={t('settings.geo.source')} value={source}
          // Switching source clears the edition so each source's own default applies: a leftover MaxMind edition would break IPinfo.
          onChange={s => patch({ geo_ip_update_source: s, geo_ip_update_edition: '' })}
          options={[
            { value: 'maxmind', label: t('settings.geo.source_maxmind') },
            { value: 'dbip', label: t('settings.geo.source_dbip') },
            { value: 'ipinfo', label: t('settings.geo.source_ipinfo') },
            { value: 'custom', label: t('settings.geo.source_custom') },
          ]} />
        {(source === 'maxmind' || source === 'ipinfo') && (
          <TextRow name="geo_ip_update_edition" label={source === 'ipinfo' ? t('settings.geo.edition_ipinfo') : t('settings.geo.edition')}
            value={v.geo_ip_update_edition} onChange={e => patch({ geo_ip_update_edition: e })} placeholder={source === 'ipinfo' ? 'ipinfo_lite' : 'GeoLite2-City'} />
        )}
        {source === 'custom' && (
          <TextRow name="geo_ip_update_url" label={t('settings.geo.url')} value={v.geo_ip_update_url} onChange={u => patch({ geo_ip_update_url: u })} placeholder="https://…/db.mmdb" />
        )}
      </Pair>

      {source !== 'dbip' && (
        <Field label={tokenLabel} hint={saved.has_geo_ip_update_token ? t('settings.geo.token_set') : undefined}>
          <SecretField id={settingId('geo_ip_update_token')} value={v.geo_ip_update_token ?? ''} isSet={saved.has_geo_ip_update_token}
            onChange={tok => patch({ geo_ip_update_token: tok })} />
        </Field>
      )}

      <div className="flex flex-wrap items-center gap-3">
        <AsyncButton variant="outline" pending={busy} onClick={update}>{t('settings.geo.update_now')}</AsyncButton>
        <p className="min-w-60 flex-1 text-xs text-muted-foreground">{t('settings.geo.update_hint')}</p>
      </div>
      <Hint>{t('settings.geo.attribution')}</Hint>
    </Group>
  )
}
