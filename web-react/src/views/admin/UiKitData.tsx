import { useEffect } from 'react'
import { Button } from '@/components/ui/button'
import { TooltipProvider } from '@/components/ui/tooltip'
import { COLOR_PRESETS } from '@/theme/presets'
import { useAppearanceStore, type Density, type ThemeMode } from '@/stores/appearance'
import { useUrlStates, urlCodecs } from '@/hooks/useUrlState'
import { ControlsDemoSection, FormsDemoSection } from './uiKitData/FormSections'
import { GraphSection, PathDiagramSection, TokensSection } from './uiKitData/DiagramSections'
import { LinesViewsSection, LongListSection, NodesTableSection, UsersServerSection } from './uiKitData/TableSections'

// Dev-only gallery for the shared data, form and diagram blocks, mounted at
// /__ui-kit/data when import.meta.env.DEV (see router/index.tsx). Sample data is
// PSP-shaped (nodes, lines, servers, users). `?section=tables|forms|diagrams`
// shows one part, and `?mode=dark&density=comfortable` sets the appearance, so a
// screenshot script can reach every combination without clicking. Strings are
// hard-coded: nothing here ships.
const SECTIONS = ['all', 'tables', 'forms', 'diagrams'] as const

export default function UiKitData() {
  const [q] = useUrlStates({
    section: urlCodecs.oneOf(SECTIONS, 'all'),
    mode: urlCodecs.oneOf(['', 'light', 'dark'] as const, ''),
    density: urlCodecs.oneOf(['', 'compact', 'comfortable'] as const, ''),
  })
  const setMode = useAppearanceStore(s => s.setMode)
  const setDensity = useAppearanceStore(s => s.setDensity)
  const setUserColor = useAppearanceStore(s => s.setUserColor)
  const mode = useAppearanceStore(s => s.mode)
  const density = useAppearanceStore(s => s.density)
  const userColor = useAppearanceStore(s => s.userColor)
  const [, patch] = useUrlStates({ section: urlCodecs.oneOf(SECTIONS, 'all') })

  useEffect(() => { if (q.mode) setMode(q.mode as ThemeMode) }, [q.mode, setMode])
  useEffect(() => { if (q.density) setDensity(q.density as Density) }, [q.density, setDensity])

  const show = (s: (typeof SECTIONS)[number]) => q.section === 'all' || q.section === s

  return (
    <TooltipProvider>
      <div className="min-h-screen bg-background p-4 text-foreground sm:p-6">
        <div className="mx-auto flex max-w-6xl flex-col gap-8">
          <header className="flex flex-col gap-3">
            <h1 className="text-2xl font-semibold">UI kit: data, forms, diagrams</h1>
            <div className="flex flex-wrap items-center gap-2 text-sm">
              {(['light', 'dark', 'auto'] as ThemeMode[]).map(m => <Button key={m} size="sm" variant={mode === m ? 'default' : 'outline'} onClick={() => setMode(m)}>{m}</Button>)}
              <span className="mx-1 h-5 w-px bg-border" />
              {(['compact', 'comfortable'] as Density[]).map(d => <Button key={d} size="sm" variant={density === d ? 'default' : 'outline'} onClick={() => setDensity(d)}>{d}</Button>)}
              <span className="mx-1 h-5 w-px bg-border" />
              {COLOR_PRESETS.map(p => (
                <Button key={p.id} size="sm" variant={userColor === p.hex ? 'default' : 'outline'} onClick={() => setUserColor(p.hex)}>
                  <span className="size-3 rounded-full" style={{ backgroundColor: p.hex }} />{p.id}
                </Button>
              ))}
              <span className="mx-1 h-5 w-px bg-border" />
              {SECTIONS.map(s => <Button key={s} size="sm" variant={q.section === s ? 'secondary' : 'ghost'} onClick={() => patch({ section: s })}>{s}</Button>)}
            </div>
          </header>
          {show('tables') && (<><NodesTableSection /><LinesViewsSection /><LongListSection /><UsersServerSection /></>)}
          {show('forms') && (<><FormsDemoSection /><ControlsDemoSection /></>)}
          {show('diagrams') && (<><PathDiagramSection /><GraphSection /><TokensSection /></>)}
        </div>
      </div>
    </TooltipProvider>
  )
}
