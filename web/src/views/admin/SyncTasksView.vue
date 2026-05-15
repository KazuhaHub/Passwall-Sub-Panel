<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import LineIcon from '@/components/LineIcon.vue'
import {
  cancelSyncTask,
  listSyncTasks,
  purgeFinishedSyncTasks,
  retrySyncTask,
  type SyncTaskListParams,
} from '@/api/syncTasks'
import type { SyncTask, SyncTaskStatus, SyncTaskType } from '@/api/types'

const items = ref<SyncTask[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(50)
const loading = ref(false)
const statusFilter = ref<SyncTaskStatus | ''>('')
const typeFilter = ref<SyncTaskType | ''>('')
const selectedItems = ref<SyncTask[]>([])
const batchBusy = ref<'retry' | 'cancel' | ''>('')

const detailDialog = ref(false)
const detailRow = ref<SyncTask | null>(null)

const statusOptions: { label: string; value: SyncTaskStatus }[] = [
  { label: '等待中', value: 'pending' },
  { label: '执行中', value: 'running' },
  { label: '已成功', value: 'succeeded' },
  { label: '已取消', value: 'canceled' },
]

const typeOptions: { label: string; value: SyncTaskType }[] = [
  { label: '删除用户', value: 'user_delete' },
  { label: '同步用户节点', value: 'user_resync' },
  { label: '同步用户配置', value: 'user_push_config' },
  { label: '创建节点', value: 'node_create' },
  { label: '删除节点', value: 'node_delete' },
  { label: '同步节点启用', value: 'node_set_enabled' },
  { label: '更新节点配置', value: 'node_update' },
]

const pendingCount = computed(() => items.value.filter((row) => statusOf(row) === 'pending').length)
const selectedCount = computed(() => selectedItems.value.length)

function idOf(row: SyncTask) {
  return row.id ?? row.ID ?? 0
}

function typeOf(row: SyncTask) {
  return row.type ?? row.Type ?? ''
}

function statusOf(row: SyncTask) {
  return row.status ?? row.Status ?? ''
}

function summaryOf(row: SyncTask) {
  return row.summary ?? row.Summary ?? ''
}

function targetOf(row: SyncTask) {
  const typ = row.target_type ?? row.TargetType ?? ''
  const id = row.target_id ?? row.TargetID ?? ''
  return `${typ}#${id}`
}

function attemptsOf(row: SyncTask) {
  return row.attempts ?? row.Attempts ?? 0
}

function nextRunOf(row: SyncTask) {
  return row.next_run_at ?? row.NextRunAt
}

function lastErrorOf(row: SyncTask) {
  return row.last_error ?? row.LastError ?? ''
}

function statusTag(status: string) {
  if (status === 'succeeded') return 'success'
  if (status === 'running') return 'warning'
  if (status === 'canceled') return 'info'
  return 'danger'
}

function formatDate(value?: string | null) {
  if (!value) return '-'
  return new Date(value).toLocaleString()
}

async function load() {
  loading.value = true
  try {
    const params: SyncTaskListParams = {
      page: page.value,
      page_size: pageSize.value,
    }
    if (statusFilter.value) params.status = statusFilter.value
    if (typeFilter.value) params.type = typeFilter.value
    const res = await listSyncTasks(params)
    items.value = res.items
    total.value = res.total
    selectedItems.value = []
  } finally {
    loading.value = false
  }
}

function handleSelectionChange(rows: SyncTask[]) {
  selectedItems.value = rows
}

async function retry(row: SyncTask) {
  await retrySyncTask(idOf(row))
  await load()
}

async function cancel(row: SyncTask) {
  await cancelSyncTask(idOf(row))
  await load()
}

async function batchRetry() {
  if (selectedItems.value.length === 0) return
  const rows = selectedItems.value.slice()
  batchBusy.value = 'retry'
  try {
    const results = await Promise.allSettled(rows.map((row) => retrySyncTask(idOf(row))))
    const failed = results.filter((result) => result.status === 'rejected').length
    if (failed > 0) {
      ElMessage.warning(`已重试 ${rows.length - failed} 个任务，失败 ${failed} 个`)
    } else {
      ElMessage.success(`已重试 ${rows.length} 个任务`)
    }
    await load()
  } finally {
    batchBusy.value = ''
  }
}

async function batchCancel() {
  if (selectedItems.value.length === 0) return
  const rows = selectedItems.value.slice()
  try {
    await ElMessageBox.confirm(`确定中止选中的 ${rows.length} 个同步任务？`, '批量中止任务', {
      type: 'warning',
    })
  } catch {
    return
  }
  batchBusy.value = 'cancel'
  try {
    const results = await Promise.allSettled(rows.map((row) => cancelSyncTask(idOf(row))))
    const failed = results.filter((result) => result.status === 'rejected').length
    if (failed > 0) {
      ElMessage.warning(`已中止 ${rows.length - failed} 个任务，失败 ${failed} 个`)
    } else {
      ElMessage.success(`已中止 ${rows.length} 个任务`)
    }
    await load()
  } finally {
    batchBusy.value = ''
  }
}

function showDetail(row: SyncTask) {
  detailRow.value = row
  detailDialog.value = true
}

async function purgeFinished() {
  try {
    await ElMessageBox.confirm(
      '清空所有非进行中（已成功 / 已取消）的同步任务记录？等待中 / 执行中的任务不受影响。',
      '清空已完成任务',
      { type: 'warning', confirmButtonText: '清空', cancelButtonText: '取消' }
    )
    const res = await purgeFinishedSyncTasks()
    ElMessage.success(`已清空 ${res.deleted} 条记录`)
    await load()
  } catch (e) {
    if (e !== 'cancel') ElMessage.error('清空失败')
  }
}

onMounted(load)
</script>

<template>
  <div class="psp-page">
    <div class="psp-page-header">
      <div>
        <div class="psp-page-title">同步任务</div>
        <div class="task-subtitle">当前列表等待重试：{{ pendingCount }}</div>
      </div>
      <div style="display: flex; gap: 8px;">
        <el-button type="danger" plain @click="purgeFinished">一键清空</el-button>
        <el-button type="primary" @click="load">
          <LineIcon name="refresh" :size="16" />
          刷新
        </el-button>
      </div>
    </div>

    <div class="psp-toolbar">
      <el-select v-model="statusFilter" clearable placeholder="状态" style="width: 150px" @change="load">
        <el-option v-for="s in statusOptions" :key="s.value" :label="s.label" :value="s.value" />
      </el-select>
      <el-select v-model="typeFilter" clearable placeholder="任务类型" style="width: 180px" @change="load">
        <el-option v-for="t in typeOptions" :key="t.value" :label="t.label" :value="t.value" />
      </el-select>
      <template v-if="selectedCount > 0">
        <el-divider direction="vertical" />
        <span class="selection-count">已选 {{ selectedCount }}</span>
        <el-button
          type="primary"
          plain
          :loading="batchBusy === 'retry'"
          :disabled="batchBusy !== ''"
          @click="batchRetry"
        >
          <LineIcon name="refresh" :size="16" />
          批量重试
        </el-button>
        <el-button
          type="danger"
          plain
          :loading="batchBusy === 'cancel'"
          :disabled="batchBusy !== ''"
          @click="batchCancel"
        >
          <LineIcon name="close" :size="16" />
          批量中止
        </el-button>
      </template>
    </div>

    <el-table v-loading="loading" :data="items" stripe @selection-change="handleSelectionChange">
      <el-table-column type="selection" width="48" />
      <el-table-column label="ID" width="80">
        <template #default="{ row }">{{ idOf(row) }}</template>
      </el-table-column>
      <el-table-column label="状态" width="110">
        <template #default="{ row }">
          <el-tag :type="statusTag(statusOf(row))">{{ statusOf(row) }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="类型" width="170">
        <template #default="{ row }">{{ typeOf(row) }}</template>
      </el-table-column>
      <el-table-column label="目标" width="130">
        <template #default="{ row }">{{ targetOf(row) }}</template>
      </el-table-column>
      <el-table-column label="摘要" min-width="220">
        <template #default="{ row }">{{ summaryOf(row) }}</template>
      </el-table-column>
      <el-table-column label="重试" width="80">
        <template #default="{ row }">{{ attemptsOf(row) }}</template>
      </el-table-column>
      <el-table-column label="下次执行" width="190">
        <template #default="{ row }">{{ formatDate(nextRunOf(row)) }}</template>
      </el-table-column>
      <el-table-column label="错误" min-width="260">
        <template #default="{ row }">
          <span class="error-text">{{ lastErrorOf(row) }}</span>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="210" fixed="right">
        <template #default="{ row }">
          <el-button size="small" @click="showDetail(row)">详情</el-button>
          <el-button size="small" type="primary" plain @click="retry(row)">重试</el-button>
          <el-button size="small" type="danger" plain @click="cancel(row)">中止</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-pagination
      v-model:current-page="page"
      v-model:page-size="pageSize"
      :total="total"
      :page-sizes="[20, 50, 100, 200]"
      layout="total, sizes, prev, pager, next"
      style="margin-top: 16px"
      @current-change="load"
      @size-change="load"
    />

    <el-dialog v-model="detailDialog" title="任务详情" width="720px" top="6vh">
      <pre v-if="detailRow" class="task-json">{{ JSON.stringify(detailRow, null, 2) }}</pre>
    </el-dialog>
  </div>
</template>

<style scoped>
.task-subtitle {
  margin-top: 6px;
  color: var(--text-muted);
  font-size: 13px;
}

.error-text {
  display: inline-block;
  max-width: 100%;
  color: #f56c6c;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  vertical-align: bottom;
}

.task-json {
  font-family: ui-monospace, 'SFMono-Regular', Menlo, Consolas, monospace;
  font-size: 12px;
  line-height: 1.55;
  background: var(--code-bg);
  color: var(--text-main);
  border: 1px solid var(--code-border);
  padding: 12px;
  border-radius: 8px;
  max-height: 520px;
  overflow: auto;
  white-space: pre;
  word-break: normal;
}

.selection-count {
  color: var(--text-muted);
  white-space: nowrap;
}
</style>
