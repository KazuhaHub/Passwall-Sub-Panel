<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import * as echarts from 'echarts/core'
import { BarChart, LineChart } from 'echarts/charts'
import { GridComponent, LegendComponent, TooltipComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import type { TrafficHistoryItem } from '@/api/traffic'

echarts.use([BarChart, LineChart, GridComponent, TooltipComponent, LegendComponent, CanvasRenderer])

const props = withDefaults(defineProps<{
  items: TrafficHistoryItem[]
  height?: number
  loading?: boolean
}>(), {
  height: 320,
  loading: false,
})

const chartEl = ref<HTMLElement | null>(null)
let chart: echarts.ECharts | null = null
let resizeObserver: ResizeObserver | null = null

const hasData = computed(() => props.items.some((item) => item.total_bytes > 0 || item.up_bytes > 0 || item.down_bytes > 0))

function formatBytes(n: number): string {
  if (!n) return '0'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = n
  let u = 0
  while (v >= 1024 && u < units.length - 1) {
    v /= 1024
    u++
  }
  return `${v.toFixed(v >= 10 || u === 0 ? 0 : 1)} ${units[u]}`
}

function renderChart() {
  if (!chartEl.value) return
  if (!chart) {
    chart = echarts.init(chartEl.value)
  }

  const labels = props.items.map((item) => item.date)
  chart.setOption({
    color: ['#14b8a6', '#3b82f6', '#f59e0b'],
    tooltip: {
      trigger: 'axis',
      valueFormatter: (value: number) => formatBytes(value),
    },
    legend: {
      top: 0,
      right: 0,
      itemWidth: 12,
      itemHeight: 8,
      textStyle: {
        color: '#64748b',
      },
    },
    grid: {
      left: 8,
      right: 16,
      top: 38,
      bottom: 8,
      containLabel: true,
    },
    xAxis: {
      type: 'category',
      data: labels,
      axisLabel: {
        color: '#64748b',
        hideOverlap: true,
      },
      axisLine: {
        lineStyle: { color: '#d8dee8' },
      },
      axisTick: { show: false },
    },
    yAxis: {
      type: 'value',
      axisLabel: {
        color: '#64748b',
        formatter: (value: number) => formatBytes(value),
      },
      splitLine: {
        lineStyle: { color: '#edf1f7' },
      },
    },
    series: [
      {
        name: '上行',
        type: 'bar',
        stack: 'traffic',
        barMaxWidth: 26,
        data: props.items.map((item) => item.up_bytes),
      },
      {
        name: '下行',
        type: 'bar',
        stack: 'traffic',
        barMaxWidth: 26,
        data: props.items.map((item) => item.down_bytes),
      },
      {
        name: '总计',
        type: 'line',
        smooth: true,
        symbolSize: 5,
        data: props.items.map((item) => item.total_bytes),
        lineStyle: { width: 2 },
      },
    ],
  })
}

watch(() => props.items, () => nextTick(renderChart), { deep: true })

onMounted(() => {
  renderChart()
  if (chartEl.value) {
    resizeObserver = new ResizeObserver(() => chart?.resize())
    resizeObserver.observe(chartEl.value)
  }
})

onBeforeUnmount(() => {
  resizeObserver?.disconnect()
  chart?.dispose()
  chart = null
})
</script>

<template>
  <div class="traffic-chart" :style="{ height: `${height}px` }">
    <div ref="chartEl" class="traffic-chart-canvas" />
    <div v-if="loading" class="chart-state">加载中</div>
    <div v-else-if="items.length === 0 || !hasData" class="chart-state">暂无流量数据</div>
  </div>
</template>

<style scoped>
.traffic-chart {
  position: relative;
  width: 100%;
  min-height: 240px;
}

.traffic-chart-canvas {
  width: 100%;
  height: 100%;
}

.chart-state {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--text-muted);
  font-size: 13px;
  background: rgba(255, 255, 255, 0.72);
  pointer-events: none;
}
</style>
