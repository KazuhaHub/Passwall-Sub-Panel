import { useMemo, useRef, useState, type ReactNode } from 'react'
import { Dialog as MuiDialog, DialogActions, DialogContent, DialogTitle, Button as MuiButton, MenuItem as MuiMenuItem, Select as MuiSelect } from '@mui/material'

import { AsyncButton, AsyncIconButton } from '@/components/common/AsyncButton'
import { CopyButton, CopyText } from '@/components/common/CopyButton'
import { FieldHint, HelpTip, InfoTip } from '@/components/common/HelpTip'
import { IconButton } from '@/components/common/IconButton'
import { KpiGrid, KpiTile } from '@/components/common/KpiTile'
import { MoreMenu } from '@/components/common/MoreMenu'
import { PageHeader } from '@/components/common/PageHeader'
import { StatusBadge } from '@/components/common/StatusBadge'
import { StatusDot } from '@/components/common/StatusDot'
import { TONES } from '@/components/common/tones'
import { confirm } from '@/components/ConfirmHost'
import { EmptyState } from '@/components/feedback/EmptyState'
import { ErrorState } from '@/components/feedback/ErrorState'
import { CardSkeleton, PageSkeleton, TableSkeleton } from '@/components/feedback/LoadingState'
import { notifySyncPending } from '@/components/feedback/syncPendingToast'
import { useToastBottomOffset } from '@/components/feedback/useToastBottomOffset'
import { pushSnack } from '@/components/SnackbarHost'
import { AppShell } from '@/components/shell/AppShell'
import { UserShell } from '@/components/shell/UserShell'
import { ADMIN_NAV } from '@/components/shell/adminNav'
import { LanguageMenu } from '@/components/shell/LanguageMenu'
import { NotificationBell } from '@/components/shell/NotificationBell'
import { ThemeMenu } from '@/components/shell/ThemeMenu'
import type { CommandProvider } from '@/components/shell/commands'
import type { NavSection } from '@/components/shell/nav'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Dialog as ShadDialog, DialogContent as ShadDialogContent, DialogDescription as ShadDialogDescription, DialogHeader as ShadDialogHeader, DialogTitle as ShadDialogTitle } from '@/components/ui/dialog'
import * as icons from '@/lib/icons'
import { IconAdd, IconDownload, IconEdit, IconSettings } from '@/lib/icons'
import type { Role } from '@/api/types'

// Dev-only gallery for the shell and the shared building blocks (mounted at
// /__ui-kit/shell only when import.meta.env.DEV, see router/index.tsx). It renders the
// REAL AppShell around itself, so the sidebar, the top bar, the palette and the menus
// can be exercised in light/dark, both densities and at phone width. Strings that are
// the gallery's own are English on purpose: nothing here ships; the components under
// test use the app's real i18n.

// The demo's nav is the admin one, so the role switch below shows what an operator sees.
const DEMO_NAV: NavSection[] = ADMIN_NAV

// A stand-in for a later package's "find a user" provider: it shows how a search plugs
// in without the palette or the shell changing.
const DEMO_PROVIDERS: CommandProvider[] = [{
  id: 'demo-users',
  heading: 'Users (demo provider)',
  order: 10,
  minQueryLength: 1,
  debounceMs: 150,
  search: async q => ['alice', 'bob', 'carol', 'dave']
    .filter(n => n.includes(q.toLowerCase()))
    .map(n => ({ id: n, label: n, description: `${n}@example.com`, icon: icons.IconUsers, to: '/__ui-kit/shell' })),
}]

function Block({ title, note, children }: { title: string; note?: string; children: ReactNode }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        {note && <CardDescription>{note}</CardDescription>}
      </CardHeader>
      <CardContent className="flex flex-wrap items-center gap-3">{children}</CardContent>
    </Card>
  )
}

const wait = (ms: number) => new Promise<void>(r => setTimeout(r, ms))

// A stand-in for a form's sticky save bar: while it is mounted the toasts sit above it.
function StickyBar() {
  const ref = useRef<HTMLDivElement>(null)
  useToastBottomOffset(ref)
  return (
    <div ref={ref} className="fixed inset-x-0 bottom-0 z-40 flex items-center justify-between border-t bg-background px-4 py-3 text-sm md:left-(--sidebar-width)">
      <span>Unsaved changes (toasts stay above this bar)</span><Button size="sm">Save</Button>
    </div>
  )
}

function Gallery({ role, setRole }: { role: Role; setRole: (r: Role) => void }) {
  const [pressed, setPressed] = useState<string | null>('failing')
  const [muiOpen, setMuiOpen] = useState(false)
  const [bar, setBar] = useState(false)
  const [shadcnOpen, setShadcnOpen] = useState(false)

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="Shell and shared blocks"
        count={12}
        description="The sidebar, top bar and palette around this page are the real shell. Try ⌘K / Ctrl K, Ctrl/Cmd+B, and narrow the window."
        primary={<Button><IconAdd />New thing</Button>}
        secondary={<Button variant="outline"><IconDownload />Export</Button>}
        more={[
          { id: 'edit', label: 'Rename', icon: IconEdit, onSelect: () => pushSnack('Rename', 'info') },
          { id: 'settings', label: 'Settings', icon: IconSettings, to: '/__ui-kit/shell' },
          { id: 'del', label: 'Delete everything', icon: icons.IconDelete, destructive: true, onSelect: () => pushSnack('Delete', 'error') },
        ]}
      />

      <Block title="Role" note="The sidebar and ⌘K filter by role with the same rule the admin layout used.">
        {(['admin', 'operator'] as const).map(r => (
          <Button key={r} variant={role === r ? 'default' : 'outline'} size="sm" onClick={() => setRole(r)}>{r}</Button>
        ))}
      </Block>

      <Block title="Status" note="Quiet by default: colour only for the abnormal. The words are always text.">
        {TONES.map(tone => <StatusBadge key={tone} tone={tone} label={`${tone}`} />)}
        <span className="w-full" />
        {TONES.map(tone => <StatusBadge key={tone} tone={tone} quiet label={`${tone} (quiet)`} />)}
        <span className="w-full" />
        {TONES.map(tone => <StatusDot key={tone} tone={tone} label={tone} hideLabel />)}
        <span className="text-xs text-muted-foreground">dots with a hidden label</span>
      </Block>

      <Block title="Buttons" note="Pending buttons disable themselves while the promise is out. Icon-only buttons all have a tooltip and aria-label.">
        <AsyncButton icon={<IconDownload />} onClick={() => wait(1500)}>Async (1.5 s)</AsyncButton>
        <AsyncIconButton label="Refresh (async)" onClick={() => wait(1500)}><icons.IconRefresh /></AsyncIconButton>
        <IconButton label="Edit"><IconEdit /></IconButton>
        <IconButton label="Delete" variant="outline"><icons.IconDelete /></IconButton>
        <CopyButton value="https://example.com/sub/abc123" name="subscription link" />
        <CopyText value="https://example.com/sub/0123456789abcdef0123456789abcdef" display="https://example.com/sub/0123…cdef" name="subscription link" />
        <MoreMenu items={[
          { id: 'a', label: 'Open', icon: icons.IconExternalLink, onSelect: () => {} },
          { id: 'b', label: 'Remove', icon: icons.IconDelete, destructive: true, onSelect: () => {} },
        ]} />
      </Block>

      <Block title="Explanations">
        <span className="flex items-center gap-1 text-sm">Help (click) <HelpTip text={'A short explanation of this page.\nSecond line.'} /></span>
        <span className="flex items-center gap-1 text-sm">Info (hover, focus, tap) <InfoTip text="The multiplier is applied to every byte of traffic." /></span>
        <span className="text-sm text-muted-foreground">Field note: <FieldHint summary="Will exceed the quota" detail="This user would pass the quota in about three days at the current rate." /></span>
      </Block>

      <div>
        <h2 className="mb-1 text-sm font-medium text-muted-foreground">KPI tiles (filter when pressed)</h2>
        <KpiGrid>
          <KpiTile label="Nodes" value="48" caption="of 64" />
          <KpiTile label="Failing" value="3" danger pressed={pressed === 'failing'} onToggle={() => setPressed(p => (p === 'failing' ? null : 'failing'))} />
          <KpiTile label="Warnings" value="7" pressed={pressed === 'warn'} onToggle={() => setPressed(p => (p === 'warn' ? null : 'warn'))} />
          <KpiTile label="Traffic" value="1.2 TB" caption="this month" />
        </KpiGrid>
      </div>

      <Block title="Feedback" note="Toasts, the confirm dialog, and the queued-sync toast. Confirm also over a MUI dialog (both libraries are on screen until the pages are migrated).">
        <Button variant="outline" onClick={() => pushSnack('Saved', 'success')}>success</Button>
        <Button variant="outline" onClick={() => pushSnack('Heads up', 'warning')}>warning</Button>
        <Button variant="outline" onClick={() => pushSnack('Could not reach the server. Check it and try again.', 'error')}>error</Button>
        <Button variant="outline" onClick={() => pushSnack('Just so you know', 'info')}>info</Button>
        <Button variant="outline" onClick={() => notifySyncPending()}>sync pending</Button>
        <Button variant="outline" onClick={() => { for (let i = 1; i <= 8; i++) pushSnack(`Error ${i}`, 'error') }}>8 errors in one tick</Button>
        <Button variant="outline" onClick={() => setBar(b => !b)}>{bar ? 'hide' : 'show'} sticky bar</Button>
        {bar && <StickyBar />}
        <Button variant="outline" onClick={() => { for (let i = 1; i <= 10_000; i++) pushSnack(`Message ${i}`, i % 50 === 0 ? 'error' : 'info') }}>10,000 messages in one tick</Button>
        <Button variant="outline" onClick={() => { for (let i = 1; i <= 3; i++) pushSnack(`Done ${i}`, 'success'); for (let i = 1; i <= 6; i++) pushSnack(`Error ${i}`, 'error') }}>3 successes then 6 errors</Button>
        <Button variant="outline" onClick={() => { for (let i = 1; i <= 200; i++) pushSnack(`Error ${i}`, 'error') }}>200 errors in one tick</Button>
        <Button variant="outline" onClick={() => void confirm({ title: 'Remove this item?', message: 'It will stop working at once.\nThis cannot be undone.' }).then(v => pushSnack(String(v), 'info'))}>confirm</Button>
        <Button variant="outline" onClick={() => void confirm({ title: 'Delete node Tokyo-1', message: 'Its 12 subscribers lose this node. This cannot be undone.', destructive: true, confirmText: 'Delete node' }).then(v => pushSnack(String(v), 'info'))}>confirm (destructive)</Button>
        <Button variant="outline" onClick={() => setMuiOpen(true)}>over a MUI dialog</Button>
        <Button variant="outline" onClick={() => setShadcnOpen(true)}>shadcn dialog</Button>
        <MuiSelect size="small" value="a" data-testid="mui-select" sx={{ minWidth: 120 }}>
          <MuiMenuItem value="a">MUI select</MuiMenuItem><MuiMenuItem value="b">Another</MuiMenuItem>
        </MuiSelect>
        <ShadDialog open={shadcnOpen} onOpenChange={setShadcnOpen}>
          <ShadDialogContent>
            <ShadDialogHeader><ShadDialogTitle>A shadcn dialog</ShadDialogTitle><ShadDialogDescription>Toasts raised while it is open are still announced.</ShadDialogDescription></ShadDialogHeader>
          </ShadDialogContent>
        </ShadDialog>
        <MuiDialog open={muiOpen} onClose={() => setMuiOpen(false)}>
          <DialogTitle>A MUI dialog</DialogTitle>
          <DialogContent>
            <MuiButton onClick={() => void confirm({ title: 'Leave this page?', message: 'You have unsaved changes.', destructive: true, confirmText: 'Leave' }).then(v => pushSnack(String(v), 'info'))}>Ask for confirmation</MuiButton>
          </DialogContent>
          <DialogActions><MuiButton onClick={() => setMuiOpen(false)}>Close</MuiButton></DialogActions>
        </MuiDialog>
      </Block>

      <div className="grid gap-4 lg:grid-cols-2">
        <EmptyState icon={<icons.IconServer />} title="No servers yet" description="Add the first server and its nodes will appear here." action={<Button><IconAdd />Add server</Button>} />
        <ErrorState reason={new Error('connect ECONNREFUSED 10.0.0.2:2053')} retry={() => wait(1200)} />
      </div>

      <div className="grid gap-4 lg:grid-cols-3">
        <Card><CardContent><PageSkeleton /></CardContent></Card>
        <Card><CardContent><TableSkeleton rows={4} cols={3} /></CardContent></Card>
        <Card><CardContent><CardSkeleton /></CardContent></Card>
      </div>

      <Block title="Icons" note="@/lib/icons: pages import these names, never lucide directly.">
        <div className="grid w-full grid-cols-[repeat(auto-fill,minmax(9rem,1fr))] gap-2">
          {Object.entries(icons).filter(([n]) => n.startsWith('Icon')).map(([name, Icon]) => {
            const C = Icon as React.ComponentType<{ className?: string }>
            return (
              <div key={name} className="flex items-center gap-2 rounded-md border px-2 py-1.5 text-xs">
                <C className="size-4 shrink-0" /><span className="truncate">{name}</span>
              </div>
            )
          })}
        </div>
      </Block>
    </div>
  )
}

export default function UiKitShell() {
  const [role, setRole] = useState<Role>('admin')
  const user = useMemo(() => ({ label: 'kazuha', role, onLogout: () => pushSnack('Sign out', 'info') }), [role])
  return (
    <AppShell
      nav={DEMO_NAV}
      role={role}
      brand={{ name: 'Passwall' }}
      user={user}
      version={{ version: 'v4.3.0-dev', channel: 'testing', commit: 'abc1234', build_date: '2026-11-19' }}
      commandProviders={DEMO_PROVIDERS}
      topBarActions={<><NotificationBell /><LanguageMenu /><ThemeMenu /></>}
    >
      <Gallery role={role} setRole={setRole} />
    </AppShell>
  )
}

// The user-side shell: no sidebar, a top bar and a single narrow column, phone first.
// Mounted at /__ui-kit/shell-user.
export function UiKitUserShell() {
  return (
    <UserShell
      brand={{ name: 'Passwall' }}
      user={{ label: 'alice', role: 'user', onLogout: () => pushSnack('Sign out', 'info') }}
      topBarActions={<><LanguageMenu /><ThemeMenu /></>}
      footer={<footer className="border-t bg-card py-3 text-center text-xs text-muted-foreground">Site footer</footer>}
    >
      <PageHeader title="My subscription" description="Link, QR code and usage first, on one screen." secondary={<Button variant="outline" size="sm"><IconDownload />Export</Button>} />
      <div className="flex flex-col gap-4">
        <Card>
          <CardHeader><CardTitle>Subscription link</CardTitle></CardHeader>
          <CardContent className="flex items-center gap-2">
            <CopyText value="https://example.com/sub/0123456789abcdef" display="https://example.com/sub/0123…cdef" name="subscription link" />
            <CopyButton value="https://example.com/sub/0123456789abcdef" name="subscription link" />
          </CardContent>
        </Card>
        <KpiGrid>
          <KpiTile label="Used" value="42.1 GB" caption="of 200 GB" />
          <KpiTile label="Expires" value="2026-12-31" />
        </KpiGrid>
      </div>
    </UserShell>
  )
}
