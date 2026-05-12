<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { topTraffic, type TrafficRow } from '@/api/traffic'

const items = ref<TrafficRow[]>([])
const loading = ref(false)
const limit = ref(20)

async function load() {
  loading.value = true
  try {
    items.value = await topTraffic(limit.value)
  } finally {
    loading.value = false
  }
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

onMounted(load)
</script>

<template>
  <div class="psp-page">
    <div class="psp-page-header">
      <div class="psp-page-title">流量看板</div>
    </div>

    <div class="psp-toolbar">
      <el-select v-model="limit" style="width: 120px" @change="load">
        <el-option label="Top 10" :value="10" />
        <el-option label="Top 20" :value="20" />
        <el-option label="Top 50" :value="50" />
      </el-select>
      <el-button @click="load">刷新</el-button>
      <span style="color: #909399; font-size: 12px">
        采集间隔来自 config.cron.traffic_pull_minutes（默认 5 min）
      </span>
    </div>

    <el-table v-loading="loading" :data="items" stripe>
      <el-table-column label="#" type="index" width="60" />
      <el-table-column prop="username" label="用户名" min-width="200" />
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
  </div>
</template>
