// ScopeOverridesEditor renders the per-field inherit/override controls for one
// scope (a group). Pure controlled component: `scope` in, `onChange` out — it
// performs no I/O. Used by the group detail Policies tab and the Settings scope
// rail. Restrict to a subset of categories via `categories`; default = all.
//
// Each row is three columns — label (flex) | toggle + state (fixed) | value
// (fixed, right-aligned) — so the switches and values line up across every row
// regardless of inherit/override state.
import { Box, MenuItem, Switch, TextField, Typography } from '@mui/material'
import { useTranslation } from 'react-i18next'

import { SCOPE_CATEGORIES, SCOPE_KEYS, type ScopeKeyMeta, type ScopeState } from './scopeOverrides'

const TOGGLE_COL = 124
const VALUE_COL = 168

export default function ScopeOverridesEditor({
  scope,
  onChange,
  categories,
  hideCategoryCaptions,
}: {
  scope: ScopeState
  onChange: (next: ScopeState) => void
  /** Restrict to these category ids; omit for all categories. */
  categories?: string[]
  /** Suppress the per-category caption (e.g. when the surrounding tab already
   *  names the single category being shown). */
  hideCategoryCaptions?: boolean
}) {
  const { t } = useTranslation(['admin', 'common'])
  const cats = categories
    ? SCOPE_CATEGORIES.filter(c => categories.includes(c.id))
    : SCOPE_CATEGORIES

  const optionLabel = (k: ScopeKeyMeta, value: string) => {
    const o = k.options?.find(o => o.value === value)
    return o ? t(`admin:groups.scope.${o.labelKey}`, { defaultValue: o.def }) : value
  }
  const defaultSuffix = () => t('admin:groups.scope.default_suffix', { defaultValue: '（默认）' })

  // Localized display of an inherited value. Bools render as the language's
  // On/Off; an enum renders its option's label, and an unset one ('') the
  // label of the value the server uses for it. A key with an unsetValue shows
  // that default for a stored ''/'0', because there 0 means "never
  // configured", not zero. Everything else shows the raw string.
  const fmtVal = (k: ScopeKeyMeta, raw: string) => {
    if (k.kind === 'bool') {
      return raw === '1'
        ? t('admin:groups.scope.on', { defaultValue: 'On' })
        : t('admin:groups.scope.off', { defaultValue: 'Off' })
    }
    if (k.kind === 'enum') {
      return raw === '' && k.enumDefault !== undefined
        ? `${optionLabel(k, k.enumDefault)}${defaultSuffix()}`
        : optionLabel(k, raw)
    }
    if (k.unsetValue !== undefined && (raw === '' || raw === '0')) {
      return `${k.unsetValue}${defaultSuffix()}`
    }
    return raw
  }

  // Switching an override on copies the inherited value, so the field opens
  // on what the admin was just looking at. For an enum whose inherited value
  // is not an option ('' = unset), that is the default it stands for — a
  // select opened on '' shows blank and would save ''.
  const seedValue = (k: ScopeKeyMeta, inherited: string) =>
    k.kind === 'enum' && k.enumDefault !== undefined && !k.options?.some(o => o.value === inherited)
      ? k.enumDefault
      : inherited

  return (
    <>
      {cats.map(cat => {
        const keys = SCOPE_KEYS.filter(k => k.cat === cat.id && scope.overridable.includes(k.key))
        if (!keys.length) return null
        return (
          <Box key={cat.id} sx={{ mt: 1.5 }}>
            {!hideCategoryCaptions && (
              <Typography variant="caption" sx={{ display: 'block', mb: 0.5, fontWeight: 600, color: 'text.secondary' }}>
                {t(`admin:groups.scope.${cat.labelKey}`, { defaultValue: cat.def })}
              </Typography>
            )}
            {keys.map(k => {
              const st = scope.edit[k.key]
              const setEdit = (v: { on: boolean; value: string }) =>
                onChange({ ...scope, edit: { ...scope.edit, [k.key]: v } })
              return (
                <Box key={k.key} sx={{ display: 'flex', alignItems: 'center', gap: 2, minHeight: 38, py: 0.25 }}>
                  <Box sx={{ flex: 1, minWidth: 0, fontSize: 14 }}>
                    {t(`admin:groups.scope.${k.labelKey}`, { defaultValue: k.def })}
                  </Box>
                  {/* toggle + state — fixed column so switches line up across rows */}
                  <Box sx={{ width: TOGGLE_COL, flexShrink: 0, display: 'flex', alignItems: 'center', gap: 1 }}>
                    <Switch size="small" checked={st.on}
                      onChange={(_, c) => setEdit({ on: c, value: c ? seedValue(k, st.value) : scope.global[k.key] })} />
                    <Typography variant="caption" sx={{ color: st.on ? 'primary.main' : 'text.secondary' }}>
                      {st.on
                        ? t('admin:groups.scope.override', { defaultValue: '覆盖' })
                        : t('admin:groups.scope.inherit', { defaultValue: '继承' })}
                    </Typography>
                  </Box>
                  {/* value — fixed column, right-aligned */}
                  <Box sx={{ width: VALUE_COL, flexShrink: 0, display: 'flex', justifyContent: 'flex-end', alignItems: 'center' }}>
                    {st.on ? (
                      k.kind === 'bool' ? (
                        <Switch size="small" checked={st.value === '1'}
                          onChange={(_, c) => setEdit({ on: true, value: c ? '1' : '0' })} />
                      ) : k.kind === 'enum' ? (
                        <TextField select size="small" fullWidth value={st.value}
                          onChange={e => setEdit({ on: true, value: e.target.value })}>
                          {(k.options ?? []).map(o => (
                            <MenuItem key={o.value} value={o.value}>
                              {t(`admin:groups.scope.${o.labelKey}`, { defaultValue: o.def })}
                            </MenuItem>
                          ))}
                        </TextField>
                      ) : k.kind === 'str' ? (
                        <TextField size="small" fullWidth value={st.value}
                          multiline={k.multiline} minRows={k.multiline ? 2 : undefined}
                          onChange={e => setEdit({ on: true, value: e.target.value })} />
                      ) : (
                        <TextField size="small" type="number" value={st.value}
                          onChange={e => setEdit({ on: true, value: e.target.value })}
                          sx={{ width: 100 }} slotProps={{
                          htmlInput: k.kind === 'float' ? { step: 'any', min: 0 } : { step: 1, min: 0 }
                        }} />
                      )
                    ) : (
                      <Typography variant="caption" color="text.secondary" sx={{ textAlign: 'right' }}>
                        {t('admin:groups.scope.global_prefix', { defaultValue: '全局' })}: {fmtVal(k, scope.global[k.key])}
                      </Typography>
                    )}
                  </Box>
                </Box>
              );
            })}
          </Box>
        );
      })}
    </>
  );
}
