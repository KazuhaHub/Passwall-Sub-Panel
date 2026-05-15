<script setup lang="ts">
import { computed, defineAsyncComponent, onMounted, ref } from 'vue'
import { listUsers } from '@/api/users'
import {
  pollTrafficNow,
  topTraffic,
  trafficHistory,
  userTrafficHistory,
  type TrafficHistoryPeriod,
  type TrafficHistoryResponse,
  type TrafficRow,
} from '@/api/traffic'
import type { User } from '@/api/types'

const TrafficChart = defineAsyncComponent(() => import('@/components/TrafficChart.vue'))

const items = ref<TrafficRow[]>([])
const users = ref<User[]>([])
const history = ref<TrafficHistoryResponse | null>(null)
const loading = ref(false)
const chartLoading = ref(false)
const pollLoading = ref(false)
const limit = ref(20)
const activeTab = ref('rank')
const selectedUserID = ref<number>(0)
const period = ref<TrafficHistoryPeriod>('day')
const rangeDays = ref(30)

const historyItems = computed(() => history.value?.items || [])
const historyTotal = computed(() => historyItems.value.reduce((sum, item) => sum + item.total_bytes, 0))
const historyUp = computed(() => historyItems.value.reduce((sum, item) => sum + item.up_bytes, 0))
const historyDown = computed(() => historyItems.value.reduce((sum, item) => sum + item.down_bytes, 0))

async function load() {
  loading.value = true
  try {
    items.value = await topTraffic(limit.value)
  } finally {
    loading.value = false
  }
}

async function loadUsers() {
  const res = await listUsers({ page: 1, page_size: 200 })
  users.value = res.items
}

async function loadHistory() {
  chartLoading.value = true
  try {
    const params = {
      period: period.value,
      since: dateString(daysAgo(rangeDays.value - 1)),
      until: dateString(new Date()),
    }
    history.value = selectedUserID.value > 0
      ? await userTrafficHistory(selectedUserID.value, params)
      : await trafficHistory(params)
  } finally {
    chartLoading.value = false
  }
}

async function pollNow() {
  pollLoading.value = true
  try {
    await pollTrafficNow()
    await Promise.all([load(), loadHistory()])
  } finally {
    pollLoading.value = false
  }
}

function daysAgo(days: number): Date {
  const d = new Date()
  d.setHours(0, 0, 0, 0)
  d.setDate(d.getDate() - days)
  return d
}

function dateString(d: Date): string {
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${y}-${m}-${day}`
}

function formatBytes(n: number): string {
  if (n === 0) return '0'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = n
  let u = 0
  while (v >= 1024 && u < units.length - 1) {
    v /= 1024
    u++
  }
  return `${v.toFixed(2)} ${units[u]}`
}

function userLabel(user: User) {
  return user.display_name ? `${user.display_name} (${user.upn})` : user.upn
}

onMounted(async () => {
  await Promise.all([load(), loadUsers(), loadHistory()])
})
</script>

<template>
  <div class="psp-page">
    <div class="psp-page-header">
      <div class="psp-page-title">流量看板</div>
      <el-button type="primary" :loading="pollLoading" @click="pollNow">
        立即采集
      </el-button>
    </div>

    <el-tabs v-model="activeTab" class="traffic-tabs">
      <el-tab-pane label="排行榜" name="rank">
        <div class="psp-toolbar">
          <el-select v-model="limit" style="width: 120px" @change="load">
            <el-option label="Top 10" :value="10" />
            <el-option label="Top 20" :value="20" />
            <el-option label="Top 50" :value="50" />
          </el-select>
          <el-button @click="load">刷新</el-button>
          <span class="toolbar-note">
            采集间隔来自系统设置，默认 5 min。
          </span>
        </div>

        <el-table v-loading="loading" :data="items" stripe>
          <el-table-column label="#" type="index" width="60" />
          <el-table-column prop="upn" label="UPN" min-width="200" />
          <el-table-column label="本周期已用" min-width="160">
            <template #default="{ row }">{{ formatBytes(row.period_used_bytes) }}</template>
          </el-table-column>
          <el-table-column label="今日已用" min-width="140">
            <template #default="{ row }">{{ formatBytes(row.today_used_bytes) }}</template>
          </el-table-column>
          <el-table-column label="累计" min-width="160">
            <template #default="{ row }">{{ formatBytes(row.permanent_total_bytes) }}</template>
          </el-table-column>
        </el-table>
      </el-tab-pane>

      <el-tab-pane label="趋势图" name="trend">
        <div class="psp-toolbar trend-toolbar">
          <el-select v-model="selectedUserID" filterable style="width: 260px" @change="loadHistory">
            <el-option label="全部用户" :value="0" />
            <el-option
              v-for="user in users"
              :key="user.id"
              :label="userLabel(user)"
              :value="user.id"
            />
          </el-select>
          <el-segmented
            v-model="period"
            :options="[
              { label: '日', value: 'day' },
              { label: '周', value: 'week' },
              { label: '月', value: 'month' },
            ]"
            @change="loadHistory"
          />
          <el-select v-model="rangeDays" style="width: 130px" @change="loadHistory">
            <el-option label="最近 7 天" :value="7" />
            <el-option label="最近 30 天" :value="30" />
            <el-option label="最近 90 天" :value="90" />
          </el-select>
          <el-button :loading="chartLoading" @click="loadHistory">刷新</el-button>
        </div>

        <div class="traffic-summary">
          <div class="metric">
            <span class="metric-label">范围总计</span>
            <strong>{{ formatBytes(historyTotal) }}</strong>
          </div>
          <div class="metric">
            <span class="metric-label">上行</span>
            <strong>{{ formatBytes(historyUp) }}</strong>
          </div>
          <div class="metric">
            <span class="metric-label">下行</span>
            <strong>{{ formatBytes(historyDown) }}</strong>
          </div>
          <div class="metric">
            <span class="metric-label">范围</span>
            <strong>{{ history?.since || '-' }} / {{ history?.until || '-' }}</strong>
          </div>
        </div>

        <div class="chart-section">
          <TrafficChart :items="historyItems" :loading="chartLoading" :height="360" />
          <p class="chart-note">
            图表基于累计快照差值生成。手动调整用量后，总量准确，上行/下行拆分仅供参考。
          </p>
        </div>
      </el-tab-pane>
    </el-tabs>
  </div>
</template>

<style scoped>
.traffic-tabs {
  margin-top: 8px;
}

.toolbar-note,
.chart-note {
  color: var(--text-muted);
  font-size: 12px;
}

.trend-toolbar {
  flex-wrap: wrap;
}

.traffic-summary {
  display: grid;
  grid-template-columns: repeat(4, minmax(140px, 1fr));
  gap: 12px;
  margin: 14px 0 18px;
}

.metric {
  border: 1px solid var(--header-border);
  border-radius: 8px;
  padding: 12px 14px;
  background: var(--panel-bg);
  min-width: 0;
}

.metric-label {
  display: block;
  color: var(--text-muted);
  font-size: 12px;
  margin-bottom: 6px;
}

.metric strong {
  display: block;
  color: var(--text-main);
  font-size: 18px;
  font-weight: 700;
  overflow-wrap: anywhere;
}

.chart-section {
  border: 1px solid var(--header-border);
  border-radius: 8px;
  padding: 16px;
  background: var(--panel-bg);
}

.chart-note {
  margin: 10px 0 0;
}

@media (max-width: 900px) {
  .traffic-summary {
    grid-template-columns: repeat(2, minmax(140px, 1fr));
  }
}

@media (max-width: 560px) {
  .traffic-summary {
    grid-template-columns: 1fr;
  }
}
</style>
