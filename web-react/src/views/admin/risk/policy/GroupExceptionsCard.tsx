import { useCallback, useEffect, useId, useRef, useState } from 'react'
import { Box, Button, CircularProgress, MenuItem, Paper, TextField, Typography, useTheme } from '@mui/material'
import { useTranslation } from 'react-i18next'
import { isAxiosError } from 'axios'

import type { Group } from '@/api/types'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'
import ScopeOverridesEditor from '@/components/scope/ScopeOverridesEditor'
import { loadScopeState, saveScopeState, SCOPE_KEYS, type ScopeState } from '@/components/scope/scopeOverrides'

const P = 'admin:risk_center.policy.'
/** The group editor's categories this page owns: the detectors'. */
const CATEGORIES = ['geo', 'geo_ban', 'risk']

interface Loaded {
  groupId: number
  /** As read: what a save diffs against and "unsaved" compares with. */
  initial: ScopeState
  /** As edited. */
  current: ScopeState
}

function errorText(err: unknown): string {
  if (isAxiosError(err)) return String((err.response?.data as { error?: string } | undefined)?.error ?? err.message)
  return err instanceof Error ? err.message : String(err)
}

function sameEdit(a: ScopeState['edit'][string] | undefined, b: ScopeState['edit'][string] | undefined): boolean {
  return a?.on === b?.on && a?.value === b?.value
}

/**
 * One group's detector exceptions, as the policy page edits them: loaded
 * whenever the picked group changes, edited locally, saved through the
 * group's own scope-settings endpoints — another resource than the policy,
 * so it has its own save (D7). The page reads `dirty` for its leave guard
 * and its unsaved-parts line.
 */
export function useGroupExceptions(groupId: number | null) {
  const { t } = useTranslation(['admin'])
  const [loaded, setLoaded] = useState<Loaded | null>(null)
  const [failed, setFailed] = useState<{ groupId: number; error: unknown } | null>(null)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    if (!groupId) return
    const ac = new AbortController()
    loadScopeState(groupId, ac.signal).then(
      st => { if (!ac.signal.aborted) setLoaded({ groupId, initial: st, current: st }) },
      error => { if (!ac.signal.aborted) setFailed({ groupId, error }) },
    )
    return () => ac.abort()
  }, [groupId])

  // Only the picked group's state counts: the previous group's stays in
  // state until the new one arrives, and must not be shown or saved as it.
  const state = loaded && loaded.groupId === groupId ? loaded : null
  const error = failed && failed.groupId === groupId && !state ? failed.error : null
  const dirty = !!state && SCOPE_KEYS.some(k => !sameEdit(state.current.edit[k.key], state.initial.edit[k.key]))

  const edit = (next: ScopeState) => setLoaded(prev => (prev ? { ...prev, current: next } : prev))

  const save = async () => {
    if (!state) return
    setSaving(true)
    try {
      await saveScopeState(state.groupId, state.current)
      const fresh = await loadScopeState(state.groupId)
      setLoaded({ groupId: state.groupId, initial: fresh, current: fresh })
      pushSnack(t(`${P}group_saved`), 'success')
    } catch {
      pushSnack(t('admin:groups.scope.save_error'), 'error')
    } finally {
      setSaving(false)
    }
  }

  /**
   * After a policy save: the rows show the GLOBAL value they inherit, which
   * the save may have moved. Read the group again, keeping every row the
   * admin has edited here, so a pending exception survives the policy save.
   */
  const reloadBaseline = useCallback(async () => {
    if (!groupId) return
    let fresh: ScopeState
    try {
      fresh = await loadScopeState(groupId)
    } catch {
      return
    }
    setLoaded(prev => {
      if (!prev || prev.groupId !== groupId) return { groupId, initial: fresh, current: fresh }
      const edit = { ...fresh.edit }
      for (const k of SCOPE_KEYS) {
        if (!sameEdit(prev.current.edit[k.key], prev.initial.edit[k.key])) edit[k.key] = prev.current.edit[k.key]
      }
      return { groupId, initial: fresh, current: { ...fresh, edit } }
    })
  }, [groupId])

  /** Back to the group as read. */
  const discard = () => setLoaded(prev => (prev ? { ...prev, current: prev.initial } : prev))

  return {
    state: state?.current ?? null, loading: !!groupId && !state && !error, error, dirty, saving,
    edit, save, discard, reloadBaseline,
  }
}

export type GroupExceptions = ReturnType<typeof useGroupExceptions>

/**
 * 分组例外 (U10): pick a group, see which detector settings it overrides and
 * what it inherits, change them and save them — without leaving the policy.
 * The group comes from the URL (`group=`), so the group dialog's link lands
 * here with the group open, and the card scrolls itself into view.
 */
export default function GroupExceptionsCard({ groups, groupId, onPickGroup, exceptions }: {
  /** The groups, or undefined while they are read. */
  groups: Group[] | undefined
  groupId: number | null
  onPickGroup: (id: number) => void
  exceptions: GroupExceptions
}) {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const titleId = useId()
  const ref = useRef<HTMLElement>(null)

  // Arrived with a group in the link: that group is why the admin is here.
  const arrivedWithGroup = useRef(groupId !== null)
  useEffect(() => {
    if (arrivedWithGroup.current) ref.current?.scrollIntoView?.({ block: 'start' })
  }, [])

  const pick = async (id: number) => {
    if (id === groupId) return
    if (exceptions.dirty) {
      const ok = await confirm({
        title: t(`${P}switch_group_title`), message: t(`${P}leave_message`),
        confirmText: t(`${P}leave_confirm`), destructive: true,
      })
      if (!ok) return
    }
    onPickGroup(id)
  }

  return (
    <Paper ref={ref} component="section" aria-labelledby={titleId} variant="outlined"
      sx={{ p: { xs: 2, sm: 2.5 }, borderRadius: 3, bgcolor: md.surfaceContainerLow }}>
      <Typography id={titleId} component="h2" sx={{ fontSize: 16, fontWeight: 600 }}>
        {t(`${P}card.groups`)}
      </Typography>
      <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant, mt: 0.5 }}>{t(`${P}card.groups_desc`)}</Typography>

      {groups?.length === 0 ? (
        <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant, mt: 2 }}>{t(`${P}no_groups`)}</Typography>
      ) : (
        // While the groups are read the picker waits, empty, rather than
        // saying there are none.
        <TextField select size="small" label={t(`${P}pick_group`)} sx={{ mt: 2, minWidth: 240 }}
          disabled={!groups}
          value={groupId !== null && groups?.some(g => g.id === groupId) ? groupId : ''}
          onChange={e => void pick(Number(e.target.value))}>
          {(groups ?? []).map(g => <MenuItem key={g.id} value={g.id}>{g.name || g.slug}</MenuItem>)}
        </TextField>
      )}

      {exceptions.loading && <Box sx={{ mt: 2 }}><CircularProgress size={20} /></Box>}
      {exceptions.error !== null && (
        <Typography sx={{ fontSize: 13, color: md.error, mt: 2 }}>
          {t('admin:risk_center.load_failed', { error: errorText(exceptions.error) })}
        </Typography>
      )}
      {exceptions.state && (
        <>
          <ScopeOverridesEditor scope={exceptions.state} onChange={exceptions.edit} categories={CATEGORIES} showHints />
          <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'flex-end', gap: 1.5, mt: 2, flexWrap: 'wrap' }}>
            {exceptions.dirty && (
              <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant }}>{t(`${P}group_dirty`)}</Typography>
            )}
            <Button variant="outlined" disabled={!exceptions.dirty || exceptions.saving}
              onClick={() => void exceptions.save()}>
              {t(`${P}group_save`)}
            </Button>
          </Box>
        </>
      )}
    </Paper>
  )
}
