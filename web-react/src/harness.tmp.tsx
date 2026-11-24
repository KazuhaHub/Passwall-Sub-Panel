import { createRoot } from 'react-dom/client'
import './styles/ui.css'
import './i18n'
import TrafficChart from '@/components/TrafficChart'
import NodeMetricsChart from '@/views/admin/servers/metrics/NodeMetricsChart'
const items = Array.from({ length: 14 }, (_, i) => ({ date: `2026-11-${String(i + 1).padStart(2, '0')}`, up_bytes: 1e9 * (1 + Math.sin(i / 2)), down_bytes: 4e9 * (1 + Math.cos(i / 3)), total_bytes: 5e9 * (1.5 + Math.sin(i / 2)) }))
const times = Array.from({ length: 30 }, (_, i) => new Date(Date.UTC(2026, 10, 20, 0, i * 10)).toISOString())
createRoot(document.getElementById('root')!).render(
  <div className="p-4 sm:p-6 space-y-4">
    <div className="rounded-xl border bg-card p-4"><TrafficChart items={items} height={300} /></div>
    <div className="rounded-xl border bg-card p-4"><NodeMetricsChart times={times} series={[{ name: 'CPU', unit: 'percent', values: times.map((_, i) => 20 + 10 * Math.sin(i / 4)) }, { name: 'Memory', unit: 'percent', values: times.map((_, i) => 50 + 5 * Math.cos(i / 5)) }]} label="CPU" /></div>
  </div>)
