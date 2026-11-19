import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { Switch } from '@/components/ui/switch'
import { Label } from '@/components/ui/label'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger,
} from '@/components/ui/dialog'
import { COLOR_PRESETS } from '@/theme/presets'
import { useAppearanceStore, type Density, type ThemeMode } from '@/stores/appearance'

// Dev-only proof that the Tailwind/shadcn stack is wired up (mounted at
// /__ui-kit only when import.meta.env.DEV, see router/index.tsx). It is the
// place to eyeball a component in light/dark, every brand preset and both
// densities before a migrated page uses it. Strings are hard-coded English on
// purpose: nothing here ships.
//
// The `psp-ui` class is what opts a subtree into the scoped reset and the
// Geist font (styles/ui.css); without it the page would inherit MUI's
// CssBaseline look.
export default function UiKitchenSink() {
  const mode = useAppearanceStore(s => s.mode)
  const density = useAppearanceStore(s => s.density)
  const userColor = useAppearanceStore(s => s.userColor)
  const setMode = useAppearanceStore(s => s.setMode)
  const setDensity = useAppearanceStore(s => s.setDensity)
  const setUserColor = useAppearanceStore(s => s.setUserColor)

  const modes: ThemeMode[] = ['light', 'dark', 'auto']
  const densities: Density[] = ['comfortable', 'compact']

  return (
    <div className="psp-ui min-h-screen p-6">
      <div className="mx-auto flex max-w-4xl flex-col gap-6">
        <header className="flex flex-col gap-1">
          <h1 className="text-2xl font-semibold">UI kit</h1>
          <p className="text-sm text-muted-foreground">
            Development only. Latin text is Geist; 中文使用系统字体。
          </p>
        </header>

        <Card>
          <CardHeader>
            <CardTitle>Appearance</CardTitle>
            <CardDescription>Drives the same store the MUI pages read.</CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            <div className="flex flex-wrap items-center gap-2">
              <span className="w-20 text-sm text-muted-foreground">Mode</span>
              {modes.map(m => (
                <Button key={m} size="sm" variant={mode === m ? 'default' : 'outline'} onClick={() => setMode(m)}>{m}</Button>
              ))}
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <span className="w-20 text-sm text-muted-foreground">Density</span>
              {densities.map(d => (
                <Button key={d} size="sm" variant={density === d ? 'default' : 'outline'} onClick={() => setDensity(d)}>{d}</Button>
              ))}
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <span className="w-20 text-sm text-muted-foreground">Brand</span>
              {COLOR_PRESETS.map(p => (
                <Button key={p.id} size="sm" variant={userColor === p.hex ? 'default' : 'outline'} onClick={() => setUserColor(p.hex)}>
                  <span className="size-3 rounded-full bg-brand" />
                  {p.id}
                </Button>
              ))}
              <Button size="sm" variant="ghost" onClick={() => setUserColor('#7A3E9D')}>custom</Button>
              <Button size="sm" variant="ghost" onClick={() => setUserColor(null)}>reset</Button>
            </div>
            <div className="flex items-center gap-3 text-sm">
              <span className="h-6 w-24 rounded-md bg-brand" />
              <span className="text-muted-foreground">--brand</span>
              <span className="h-(--control-h) w-24 rounded-md border bg-muted px-2 text-xs leading-(--control-h)">--control-h</span>
              <span className="h-(--row-h) w-24 rounded-md border bg-muted px-2 text-xs leading-(--row-h)">--row-h</span>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader><CardTitle>Buttons and badges</CardTitle></CardHeader>
          <CardContent className="flex flex-col gap-4">
            <div className="flex flex-wrap gap-2">
              <Button>Default</Button>
              <Button variant="secondary">Secondary</Button>
              <Button variant="outline">Outline</Button>
              <Button variant="ghost">Ghost</Button>
              <Button variant="destructive">Destructive</Button>
              <Button variant="link">Link</Button>
              <Button disabled>Disabled</Button>
            </div>
            <div className="flex flex-wrap gap-2">
              <Badge>Default</Badge>
              <Badge variant="secondary">Secondary</Badge>
              <Badge variant="outline">Outline</Badge>
              <Badge variant="destructive">Destructive</Badge>
            </div>
            <div className="flex flex-wrap items-center gap-3">
              <div className="flex items-center gap-2">
                <Switch id="ui-kit-switch" defaultChecked />
                <Label htmlFor="ui-kit-switch">Switch</Label>
              </div>
              <Input className="h-(--control-h) max-w-60" placeholder="Input (height follows density)" />
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader><CardTitle>Table, tabs and dialog</CardTitle></CardHeader>
          <CardContent>
            <Tabs defaultValue="table">
              <TabsList>
                <TabsTrigger value="table">Table</TabsTrigger>
                <TabsTrigger value="dialog">Dialog</TabsTrigger>
              </TabsList>
              <TabsContent value="table">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Node</TableHead>
                      <TableHead>Status</TableHead>
                      <TableHead className="text-right">Traffic</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {[['tokyo-1', 'online', '1.2 TB'], ['frankfurt-2', 'online', '640 GB'], ['香港-3', 'offline', '0 B']].map(([n, s, t]) => (
                      <TableRow key={n} className="h-(--row-h)">
                        <TableCell className="font-medium">{n}</TableCell>
                        <TableCell><Badge variant={s === 'online' ? 'secondary' : 'destructive'}>{s}</Badge></TableCell>
                        <TableCell className="text-right tabular-nums">{t}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </TabsContent>
              <TabsContent value="dialog" className="pt-4">
                <Dialog>
                  <DialogTrigger render={<Button variant="outline" />}>Open dialog</DialogTrigger>
                  <DialogContent>
                    <DialogHeader>
                      <DialogTitle>Dialog</DialogTitle>
                      <DialogDescription>Portaled to body; it must still get Geist and the reset.</DialogDescription>
                    </DialogHeader>
                    <DialogFooter>
                      <Button variant="outline">Cancel</Button>
                      <Button>Confirm</Button>
                    </DialogFooter>
                  </DialogContent>
                </Dialog>
              </TabsContent>
            </Tabs>
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
