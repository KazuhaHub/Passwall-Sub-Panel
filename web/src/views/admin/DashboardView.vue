<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { listUsers } from '@/api/users'
import { listNodes } from '@/api/nodes'
import { listGroups } from '@/api/groups'
import { topTraffic, type TrafficRow } from '@/api/traffic'

const userCount = ref(0)
const nodeCount = ref(0)
const groupCount = ref(0)
const topUsers = ref<TrafficRow[]>([])
const loading = ref(true)

onMounted(async () => {
  try {
    const [u, n, g, top] = await Promise.all([
      listUsers({ page: 1, page_size: 1 }),
      listNodes(),
      listGroups(),
      topTraffic(5).catch(() => []),
    ])
    userCount.value = u.total
    nodeCount.value = n.length
    groupCount.value = g.items.length
    topUsers.value = top
  } finally {
    loading.value = false
  }
})

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
</script>

<template>
  <div class="dashboard">
    <div class="page-title">总览</div>

    <div v-loading="loading" class="stat-grid">
      <div class="stat-card variant-users">
        <div class="stat-icon"><el-icon><User /></el-icon></div>
        <div class="stat-body">
          <div class="stat-label">用户总数</div>
          <div class="stat-value">{{ userCount }}</div>
        </div>
      </div>
      <div class="stat-card variant-nodes">
        <div class="stat-icon"><el-icon><Connection /></el-icon></div>
        <div class="stat-body">
          <div class="stat-label">节点总数</div>
          <div class="stat-value">{{ nodeCount }}</div>
        </div>
      </div>
      <div class="stat-card variant-groups">
        <div class="stat-icon"><el-icon><Files /></el-icon></div>
        <div class="stat-body">
          <div class="stat-label">分组总数</div>
          <div class="stat-value">{{ groupCount }}</div>
        </div>
      </div>
    </div>

    <div class="panel">
      <div class="panel-header">
        <div class="panel-title">本周期流量排行</div>
        <router-link to="/admin/traffic" class="panel-link">查看全部 →</router-link>
      </div>
      <el-table v-if="topUsers.length" :data="topUsers" stripe>
        <el-table-column label="#" type="index" width="60" />
        <el-table-column prop="upn" label="UPN" min-width="200" />
        <el-table-column label="本周期已用" min-width="160">
          <template #default="{ row }">{{ formatBytes(row.period_used_bytes) }}</template>
        </el-table-column>
        <el-table-column label="今日已用" min-width="160">
          <template #default="{ row }">{{ formatBytes(row.today_used_bytes) }}</template>
        </el-table-column>
      </el-table>
      <el-empty v-else description="暂无流量数据（采集需等首个 5min 窗口）" :image-size="80" />
    </div>
  </div>
</template>

<style scoped>
.dashboard {
  color: var(--text-main);
}

.page-title {
  font-size: 22px;
  font-weight: 700;
  margin-bottom: 24px;
}

.stat-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
  gap: 20px;
  margin-bottom: 28px;
}

.stat-card {
  background: var(--card-bg);
  border: 1px solid var(--header-border);
  border-radius: 16px;
  padding: 24px;
  display: flex;
  align-items: center;
  gap: 20px;
  transition: var(--transition);
}

.stat-card:hover {
  transform: translateY(-4px);
  box-shadow: 0 12px 24px rgba(99, 102, 241, 0.12);
}

.stat-icon {
  width: 56px;
  height: 56px;
  border-radius: 14px;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 24px;
  color: #fff;
  flex-shrink: 0;
}

.variant-users .stat-icon {
  background: linear-gradient(135deg, #6366f1 0%, #8b5cf6 100%);
}

.variant-nodes .stat-icon {
  background: linear-gradient(135deg, #3b82f6 0%, #2dd4bf 100%);
}

.variant-groups .stat-icon {
  background: linear-gradient(135deg, #f59e0b 0%, #ef4444 100%);
}

.stat-label {
  font-size: 13px;
  color: var(--text-muted);
  margin-bottom: 4px;
}

.stat-value {
  font-size: 32px;
  font-weight: 700;
}

.panel {
  background: var(--card-bg);
  border: 1px solid var(--header-border);
  border-radius: 16px;
  padding: 24px;
}

.panel-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 16px;
}

.panel-title {
  font-size: 16px;
  font-weight: 600;
}

.panel-link {
  font-size: 13px;
  color: #6366f1;
}

.panel-link:hover {
  text-decoration: underline;
}
</style>
