import { useState } from 'react'
import {
  Box, Button, Dialog, DialogActions, DialogContent, DialogTitle, FormControlLabel, Radio, RadioGroup, Typography, useMediaQuery, useTheme,
} from '@mui/material'
import { useTranslation } from 'react-i18next'

import { destErrorOf } from '@/api/accessControl'
import { AsyncButton } from '@/components/AsyncButton'
import FieldHint from '@/components/FieldHint'
import { pushSnack } from '@/components/SnackbarHost'
import { useAddDestException, useDestStatus } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { applyMinutes } from '@/utils/accessControl'
import { destErrorText } from '../destErrors'

const E = 'admin:access_control.records.exception.'

export interface ExceptionTarget {
  /** The destination host of the hit (no port). */
  host: string
  /** The account that hit it; null when it is gone. */
  user: { id: number; upn: string } | null
  /** The allowlist group that refused it (a `g<id>` source), when one did: the exception can go to that group alone. */
  group?: { id: number; name: string } | null
}

/**
 * "Allow exception" from a hit (S12): let a destination through for everyone
 * (the GLOBAL exception: the server makes the exceptions list and the allow
 * policy over it the first time, in one transaction), or only exempt the
 * account that hit it. On a hit refused by an allowlist group the default is
 * that group's supplement list (only its members), never everyone. The global
 * choice says what it overrides; the exemption choice hands over to the exemption dialog,
 * which owns the reason and the expiry (S5), instead of copying its form here.
 */
export default function AddExceptionDialog({ target, onClose, onExempt }: {
  target: ExceptionTarget | null
  onClose: () => void
  /** The admin chose "exempt only this account": open the exemption dialog for it. */
  onExempt: (userId: number) => void
}) {
  const { t } = useTranslation(['admin'])
  const theme = useTheme()
  const md = theme.palette.md
  const phone = useMediaQuery(theme.breakpoints.down('sm'))
  const scope = useQueryScope()
  const add = useAddDestException(scope)
  const status = useDestStatus(scope, { enabled: target !== null })
  const [match, setMatch] = useState<'site' | 'host'>('site')
  const [choice, setChoice] = useState<'global' | 'exempt' | 'group' | null>(null)
  const where = choice ?? (target?.group ? 'group' : 'global')
  const setWhere = (v: 'global' | 'exempt' | 'group') => setChoice(v)
  const [error, setError] = useState<string | null>(null)
  const minutes = applyMinutes(status.data)

  const close = () => { setError(null); setChoice(null); setMatch('site'); onClose() }

  const save = async () => {
    if (!target) return
    if (where === 'exempt' && target.user) { onExempt(target.user.id); close(); return }
    setError(null)
    try {
      const r = await add.mutateAsync({ target: target.host, match, ...(where === 'group' && target.group ? { group_id: target.group.id } : {}) })
      pushSnack(where === 'group' && target.group
        ? t(`${E}saved_group`, { group: target.group.name, entry: r.entry })
        : t(r.created ? `${E}saved_created` : `${E}saved`, { entry: r.entry }), 'success')
      close()
    } catch (err) {
      setError(destErrorOf(err)?.code === 'dest_exception_invalid' ? t(`${E}invalid`) : destErrorText(t, err))
    }
  }

  return (
    <Dialog open={target !== null} onClose={close} fullWidth maxWidth="sm" fullScreen={phone}
      slotProps={{ paper: { sx: { bgcolor: md.surfaceContainerHigh, borderRadius: phone ? 0 : 3 } } }}>
      <DialogTitle>{t(`${E}title`, { dest: target?.host ?? '' })}</DialogTitle>
      <DialogContent sx={{ display: 'grid', gap: 1.5 }}>
        <Box>
          <Typography variant="body2" sx={{ fontWeight: 600 }}>{t(`${E}where`)}</Typography>
          <RadioGroup value={where} onChange={(_, v) => setWhere(v as 'global' | 'exempt' | 'group')} aria-label={t(`${E}where`)}>
            {target?.group && <FormControlLabel value="group" control={<Radio />} label={t(`${E}group`, { group: target.group.name })} />}
            <FormControlLabel value="global" control={<Radio />} label={t(`${E}global`)} />
            {where === 'global' && (
              <Box sx={{ pl: 4, display: 'grid', gap: 1 }}>
                <FieldHint tone="amber" summary={t(`${E}global_hint.summary`)} detail={t(`${E}global_hint.detail`)} />
                <Typography variant="caption" sx={{ color: md.onSurfaceVariant }}>{t(`${E}first_time`)}</Typography>
              </Box>
            )}
            {target?.user && (
              <FormControlLabel value="exempt" control={<Radio />} label={t(`${E}exempt`, { upn: target.user.upn })} />
            )}
            {where === 'exempt' && (
              <Box sx={{ pl: 4 }}>
                <FieldHint tone="amber" summary={t(`${E}exempt_hint.summary`)} detail={t(`${E}exempt_hint.detail`)} />
              </Box>
            )}
          </RadioGroup>
        </Box>
        {(where === 'global' || where === 'group') && (
          <Box>
            <Typography variant="body2" sx={{ fontWeight: 600 }}>{t(`${E}match`)}</Typography>
            <RadioGroup row value={match} onChange={(_, v) => setMatch(v as 'site' | 'host')} aria-label={t(`${E}match`)}>
              <FormControlLabel value="site" control={<Radio />} label={t('admin:access_control.test.exception.site')} />
              <FormControlLabel value="host" control={<Radio />} label={t('admin:access_control.test.exception.host')} />
            </RadioGroup>
          </Box>
        )}
        {error && <Typography variant="body2" role="alert" sx={{ color: md.error }}>{error}</Typography>}
        {where !== 'exempt' && (
          <Typography variant="caption" sx={{ color: md.onSurfaceVariant }}>
            {minutes === null ? t(`${E}eta_unknown`) : t(`${E}eta`, { count: minutes })}
          </Typography>
        )}
      </DialogContent>
      <DialogActions>
        <Button onClick={close}>{t(`${E}cancel`)}</Button>
        <AsyncButton variant="contained" onClick={save}>{where === 'exempt' ? t(`${E}exempt_action`) : t(`${E}allow`)}</AsyncButton>
      </DialogActions>
    </Dialog>
  )
}
