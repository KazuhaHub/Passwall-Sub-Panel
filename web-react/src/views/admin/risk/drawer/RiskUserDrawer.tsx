import { useId, useState } from 'react'
import {
  Box, Button, Drawer, IconButton, Menu, MenuItem, Skeleton, Tab, Tabs, Typography, useMediaQuery, useTheme,
  type Theme,
} from '@mui/material'
import { ThemeProvider } from '@mui/material/styles'
import CloseIcon from '@mui/icons-material/Close'
import MoreHorizIcon from '@mui/icons-material/MoreHoriz'
import { Link as RouterLink } from 'react-router'
import { useTranslation } from 'react-i18next'
import { isAxiosError } from 'axios'

import { useRiskUser } from '@/query/riskCenter'
import { useQueryScope } from '@/query/useQueryScope'
import { availableActions, otherHold, subjectOfSummary, type RiskActionKind, type RiskSubject } from '../actions/riskSubject'
import { useRiskActions } from '../actions/useRiskActions'
import ConnectionsTab from './ConnectionsTab'
import DevicesTab from './DevicesTab'
import DrawerHeader from './DrawerHeader'
import OverviewTab from './OverviewTab'
import TimelineTab from './TimelineTab'
import AccessTab from './AccessTab'
import { useCan } from '@/utils/permissions'

/**
 * Above every modal. MUI's Drawer sits at zIndex.drawer (1200), below a
 * Dialog (zIndex.modal, 1300): opened from the Users edit dialog it would
 * render BEHIND it. One above, MUI's modal manager makes the drawer the top
 * modal (the focus trap moves to it) and hands back to the dialog on close.
 */
export const riskDrawerZIndex = (t: Theme) => t.zIndex.modal + 1

/**
 * The theme INSIDE the drawer, with modals raised above it: every menu,
 * select, autocomplete and dialog a tab opens (the records' filters, the ⋯
 * menu, the action dialogs) stacks by zIndex.modal, and at the default it
 * would open behind the drawer that opened it.
 */
const aboveDrawer = (outer: Theme): Theme => ({ ...outer, zIndex: { ...outer.zIndex, modal: outer.zIndex.modal + 2 } })

type DrawerTab = 'overview' | 'connections' | 'devices' | 'timeline' | 'access'
const TABS: readonly DrawerTab[] = ['overview', 'connections', 'devices', 'timeline', 'access']

/** The actions shown as buttons; the rest (undo, trust) sit behind ⋯. */
const PRIMARY: readonly RiskActionKind[] = ['pause', 'convert_manual', 'resume', 'dismiss', 'redismiss']

function ActionBar({ subject, host, onStart, disabled = false }: {
  subject: RiskSubject
  host: 'risk' | 'users' | 'access'
  onStart: (kind: RiskActionKind, subject: RiskSubject) => void
  disabled?: boolean
}) {
  const { t } = useTranslation(['admin'])
  const [anchor, setAnchor] = useState<HTMLElement | null>(null)
  const actions = availableActions(subject, 'drawer')
  const primary = actions.filter(a => PRIMARY.includes(a))
  const more = actions.filter(a => !PRIMARY.includes(a))
  const label = (k: RiskActionKind) => t(`admin:risk_center.actions.${k}`)
  return (
    <Box sx={{ px: 2.5, pb: 1.5, display: 'flex', gap: 1, flexWrap: 'wrap', alignItems: 'center' }}>
      {primary.map(k => (
        <Button disabled={disabled} key={k} size="small" variant={k === 'pause' || k === 'convert_manual' ? 'outlined' : 'contained'}
          color={k === 'pause' || k === 'convert_manual' ? 'error' : 'primary'} sx={{ minWidth: 44, minHeight: 44 }} onClick={() => onStart(k, subject)}>
          {label(k)}
        </Button>
      ))}
      {/* A hold the risk center did not write is not its to lift: the Users
          page owns it. Not offered on the Users page itself. */}
      {otherHold(subject) && host !== 'users' && (
        <Button disabled={disabled} size="small" component={RouterLink} to={`/admin/users?q=${encodeURIComponent(subject.upn)}`}
          sx={{ textTransform: 'none', minWidth: 44, minHeight: 44 }}>
          {t('admin:risk_center.drawer.open_users')}
        </Button>
      )}
      {more.length > 0 && (
        <>
          <IconButton disabled={disabled} size="small" aria-label={t('admin:risk_center.actions.more')} aria-haspopup="menu"
            sx={{ minWidth: 44, minHeight: 44 }} onClick={e => setAnchor(e.currentTarget)}>
            <MoreHorizIcon fontSize="small" />
          </IconButton>
          <Menu anchorEl={anchor} open={anchor !== null} onClose={() => setAnchor(null)}>
            {more.map(k => (
              <MenuItem disabled={disabled} key={k} sx={{ minHeight: 44 }} onClick={() => { setAnchor(null); onStart(k, subject) }}>{label(k)}</MenuItem>
            ))}
          </Menu>
        </>
      )}
    </Box>
  )
}

/** The body before the summary arrives: the header's and the rows' shapes. */
function Loading() {
  return (
    <Box sx={{ p: 2.5, display: 'flex', flexDirection: 'column', gap: 1.25 }}>
      <Skeleton variant="text" width="40%" height={32} />
      <Skeleton variant="text" width="70%" />
      {[0, 1, 2, 3, 4].map(i => <Skeleton key={i} variant="rounded" height={64} />)}
    </Box>
  )
}

/**
 * ONE ACCOUNT'S RISK DETAIL, over whichever page opened it: who it is and
 * where its service stands, what can be done about it (the one action
 * matrix the queue's row menu also uses), and tabs — every detector's
 * verdict, where it connected from, the devices behind its fetches, and its
 * timeline and administrator destination access.
 *
 * Permanently mounted, like AccountSecurityDrawer: `shown` keeps the last
 * account so the close slide has content, and each open — or a switch to
 * another account — starts on the host's initial tab with no dialog left open. It reads
 * nothing while closed.
 */
export default function RiskUserDrawer({ userId, onClose, host, initialTab }: {
  userId: number | null
  onClose: () => void
  /** 'users' when the Users page hosts it: its link back to that page is
   *  left out. */
  host: 'risk' | 'users' | 'access'
  initialTab?: DrawerTab
}) {
  const { t } = useTranslation(['admin'])
  const theme = useTheme()
  const md = theme.palette.md
  const phone = useMediaQuery(theme.breakpoints.down('sm'))
  const scope = useQueryScope()
  const canAccess = useCan('access.view')
  const requestedTab = initialTab ?? (host === 'access' ? 'access' : 'overview')
  const firstTab = requestedTab === 'access' && !canAccess ? 'overview' : requestedTab
  const headingId = useId()
  const actions = useRiskActions()
  const [accessBusy, setAccessBusy] = useState(false)
  const close = () => { if (!accessBusy) onClose() }
  const open = userId !== null

  const [shown, setShown] = useState<number | null>(userId)
  if (userId !== null && userId !== shown) setShown(userId)

  const [tab, setTab] = useState<DrawerTab>(firstTab)
  // Each open edge and each switch of account resets the transient UI.
  const openKey = open ? `${userId}:${firstTab}` : ''
  const [seenKey, setSeenKey] = useState(openKey)
  if (openKey !== seenKey) {
    setSeenKey(openKey)
    setTab(firstTab)
    actions.cancel()
  }

  const q = useRiskUser(scope, shown ?? 0, { enabled: open })
  const data = q.data
  const visibleTab = tab === 'access' && !canAccess ? 'overview' : tab

  let body
  if (data) {
    body = (
      <>
        <DrawerHeader summary={data} headingId={headingId} onClose={close} disabled={accessBusy} />
        <ActionBar subject={subjectOfSummary(data)} host={host} onStart={actions.start} disabled={accessBusy} />
        <Tabs value={visibleTab} onChange={(_, v: DrawerTab) => setTab(v)} variant={phone ? 'scrollable' : 'fullWidth'} scrollButtons={phone ? false : 'auto'}
          sx={{ px: 1, borderBottom: `1px solid ${md.outlineVariant}` }}>
          {TABS.filter(k => k !== 'access' || canAccess).map(k => <Tab disabled={accessBusy} key={k} value={k} label={t(`admin:risk_center.drawer.tab_${k}`)} />)}
        </Tabs>
        <Box sx={{ flex: 1, overflowY: 'auto', p: 2 }}>
          {visibleTab === 'overview' && <OverviewTab summary={data} />}
          {visibleTab === 'connections' && <ConnectionsTab summary={data} />}
          {visibleTab === 'devices' && <DevicesTab summary={data} />}
          {visibleTab === 'timeline' && <TimelineTab userId={data.user.id} upn={data.user.upn} />}
          {open && visibleTab === 'access' && <AccessTab key={data.user.id} userId={data.user.id} upn={data.user.upn} onBusyChange={setAccessBusy} />}
        </Box>
      </>
    )
  } else if (q.isError) {
    const status = isAxiosError(q.error) ? q.error.response?.status : undefined
    const serverError = isAxiosError(q.error)
      ? String((q.error.response?.data as { error?: string } | undefined)?.error ?? q.error.message)
      : String(q.error)
    body = (
      <Box sx={{ p: 2.5, display: 'flex', flexDirection: 'column', gap: 1.5 }}>
        <Box sx={{ display: 'flex', justifyContent: 'flex-end' }}>
          <IconButton onClick={onClose} aria-label={t('admin:risk_center.drawer.close')} sx={{ minWidth: 44, minHeight: 44 }}><CloseIcon /></IconButton>
        </Box>
        {status === 404 ? (
          <Typography id={headingId} sx={{ fontSize: 14, color: md.onSurfaceVariant }}>
            {t('admin:risk_center.drawer.not_found')}
          </Typography>
        ) : status === 503 ? (
          <Typography id={headingId} sx={{ fontSize: 14, color: md.onSurfaceVariant }}>
            {t('admin:risk_center.unwired')}
          </Typography>
        ) : (
          <>
            <Typography id={headingId} sx={{ fontSize: 14, color: md.error }}>
              {t('admin:risk_center.load_failed', { error: serverError })}
            </Typography>
            <Button variant="outlined" size="small" sx={{ alignSelf: 'flex-start', minWidth: 44, minHeight: 44 }} onClick={() => void q.refetch()}>
              {t('admin:risk_center.retry')}
            </Button>
          </>
        )}
      </Box>
    )
  } else {
    body = <Loading />
  }

  return (
    <Drawer anchor="right" open={open} onClose={close} transitionDuration={{ enter: 300, exit: 240 }}
      sx={{ zIndex: riskDrawerZIndex }}
      slotProps={{
        paper: {
          role: 'dialog', 'aria-labelledby': headingId,
          sx: {
            width: phone ? '100vw' : 560, maxWidth: '100vw', bgcolor: md.surfaceContainerLow,
            display: 'flex', flexDirection: 'column',
            ...(phone ? {} : { borderTopLeftRadius: 16, borderBottomLeftRadius: 16 }),
          },
        },
      }}>
      <ThemeProvider theme={aboveDrawer}>
        {shown !== null && body}
        {actions.dialogs}
      </ThemeProvider>
    </Drawer>
  )
}
