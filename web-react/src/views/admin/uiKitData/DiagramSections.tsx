import { useState } from 'react'
import { LandingColorBar } from '@/components/diagram/LandingColorBar'
import { assignLandingColors } from '@/components/diagram/landingColor'
import { PathDiagram } from '@/components/diagram/PathDiagram'
import { RelationGraph } from '@/components/diagram/RelationGraph'
import { describePath } from '@/components/diagram/pathSummary'
import { useChartTokens } from '@/hooks/useChartTokens'
import { useTranslation } from 'react-i18next'
import { LINES, graphSample, landingColorsFor, linePath } from './sampleData'
import { Section } from './TableSections'

export function PathDiagramSection() {
  const { t } = useTranslation('common')
  const colors = landingColorsFor(LINES)
  const rows = [
    { title: '端口转发（正常）', line: LINES[0], ends: true },
    { title: '加密隧道', line: LINES[1], ends: true },
    { title: '等待生效', line: LINES[4], ends: true },
    { title: 'CDN', line: LINES[5], ends: false },
    { title: '外部入口（异常）', line: LINES[6], ends: false },
    { title: '直连', line: LINES[2], ends: false },
  ]
  return (
    <Section id="paths" title="路径图" note="实线是端口转发，虚线是加密隧道；连线上的文字是延迟或状态。每张图的读屏名称是下面那句生成的话。">
      <div className="grid gap-4 lg:grid-cols-2">
        {rows.map(r => {
          const { nodes, edges } = linePath(r.line, r.ends, colors)
          return (
            <div key={r.title} className="flex min-w-0 flex-col gap-3 rounded-xl border bg-card p-4">
              <span className="text-xs font-medium text-muted-foreground">{r.title}</span>
              <PathDiagram nodes={nodes} edges={edges} />
              <p className="text-xs text-muted-foreground">朗读：{describePath(nodes, edges, t)}</p>
            </div>
          )
        })}
      </div>
      <div className="grid gap-4 lg:grid-cols-2">
        <div className="flex min-w-0 flex-col gap-3 rounded-xl border bg-card p-4">
          <span className="text-xs font-medium text-muted-foreground">窄容器（280px）自动改为竖排</span>
          <div className="w-[280px] max-w-full rounded-md border border-dashed p-2">
            <PathDiagram {...linePath(LINES[1], true, colors)} />
          </div>
        </div>
        <div className="flex min-w-0 flex-col gap-3 rounded-xl border bg-card p-4">
          <span className="text-xs font-medium text-muted-foreground">表格里的小图（size=sm）</span>
          <div className="flex flex-col gap-2">
            {LINES.filter(l => l.kind === 'line').slice(0, 6).map(l => (
              <div key={l.id} className="flex h-(--row-h) items-center gap-2 border-b last:border-0">
                <LandingColorBar color={colors.get(String(l.landingId))} className="h-5 self-center" />
                <PathDiagram {...linePath(l, false, colors)} size="sm" />
              </div>
            ))}
          </div>
        </div>
      </div>
    </Section>
  )
}

export function GraphSection() {
  const big = graphSample(30)
  const small = graphSample(6)
  const [sel, setSel] = useState<string | null>('e3')
  return (
    <Section id="graph" title="关系图" note="选中一个框，经过它的连线保持清晰，其余变淡。每列 30 个框时滚动，不缩小；窄屏改为列表。图下方的连线列表始终可读、可聚焦。">
      <RelationGraph columns={small.columns} edges={small.edges} selectedId={sel} onSelect={setSel} maxHeight="22rem" />
      <RelationGraph columns={big.columns} edges={big.edges} maxHeight="30rem" />
    </Section>
  )
}

export function TokensSection() {
  const tk = useChartTokens()
  const palette = assignLandingColors(Array.from({ length: 11 }, (_, i) => i + 1))
  const swatch = (name: string, color: string) => (
    <div key={name} className="flex items-center gap-2 text-xs">
      <span className="size-5 rounded border" style={{ backgroundColor: color }} />
      <span className="font-mono text-muted-foreground">{name}</span>
    </div>
  )
  return (
    <Section id="tokens" title="图表与落地颜色" note="useChartTokens 读出的颜色，随明暗和主题色变化；下面一行是落地节点的八种颜色。">
      <div className="flex flex-wrap gap-x-5 gap-y-2 rounded-xl border bg-card p-4">
        {tk.series.map((c, i) => swatch(`series-${i + 1}`, c))}
        {swatch('text', tk.text)}
        {swatch('mutedText', tk.mutedText)}
        {swatch('grid', tk.grid)}
        {swatch('tooltipBg', tk.tooltipBg)}
        {swatch('success', tk.success)}
        {swatch('warning', tk.warning)}
        {swatch('danger', tk.danger)}
      </div>
      <div className="flex flex-wrap items-center gap-3 rounded-xl border bg-card p-4">
        {[...palette].map(([id, color]) => (
          <span key={id} className="flex h-6 items-center gap-1.5 text-xs"><LandingColorBar color={color} className="h-5" />落地 #{id}</span>
        ))}
      </div>
    </Section>
  )
}
