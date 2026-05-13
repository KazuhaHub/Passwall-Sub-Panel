<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { clearAudit, listAudit, type AuditEntry } from '@/api/audit'

const items = ref<AuditEntry[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(50)
const loading = ref(false)
const actorFilter = ref('')
const actionFilter = ref('')
const clearing = ref(false)

const detailDialog = ref(false)
const detailRow = ref<AuditEntry | null>(null)

async function load() {
  loading.value = true
  try {
    const res = await listAudit({
      page: page.value,
      page_size: pageSize.value,
      actor: actorFilter.value || undefined,
      action: actionFilter.value || undefined,
    })
    items.value = res.items
    total.value = res.total
  } finally {
    loading.value = false
  }
}

function showDetail(row: AuditEntry) {
  detailRow.value = row
  detailDialog.value = true
}

function formatJSON(s: string): string {
  if (!s) return ''
  try {
    return JSON.stringify(JSON.parse(s), null, 2)
  } catch {
    return s
  }
}

function formatDate(value?: string) {
  if (!value) return '-'
  const d = new Date(value)
  return Number.isNaN(d.getTime()) ? '-' : d.toLocaleString()
}

async function clearAll() {
  await ElMessageBox.confirm('确定清空所有审计日志？此操作不可恢复。', '清空审计日志', {
    type: 'warning',
    confirmButtonText: '清空',
    cancelButtonText: '取消',
  })
  clearing.value = true
  try {
    await clearAudit()
    ElMessage.success('已清空')
    await load()
  } finally {
    clearing.value = false
  }
}

onMounted(load)
</script>

<template>
  <div class="psp-page">
    <div class="psp-page-header">
      <div class="psp-page-title">审计日志</div>
      <el-button type="danger" plain :loading="clearing" @click="clearAll">清空所有</el-button>
    </div>

    <div class="psp-toolbar">
      <el-input
        v-model="actorFilter"
        placeholder="按操作者筛选"
        style="width: 200px"
        clearable
        @change="load"
      />
      <el-input
        v-model="actionFilter"
        placeholder="按动作筛选"
        style="width: 240px"
        clearable
        @change="load"
      />
      <el-button @click="load">刷新</el-button>
    </div>

    <el-table v-loading="loading" :data="items" stripe>
      <el-table-column label="时间" min-width="180">
        <template #default="{ row }">{{ formatDate(row.at) }}</template>
      </el-table-column>
      <el-table-column prop="actor" label="操作者" min-width="160" />
      <el-table-column prop="action" label="动作" min-width="180" />
      <el-table-column prop="target" label="对象" min-width="200" />
      <el-table-column prop="ip" label="IP" width="140" />
      <el-table-column label="详情" width="80">
        <template #default="{ row }">
          <el-button size="small" @click="showDetail(row)">查看</el-button>
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

    <el-dialog v-model="detailDialog" title="审计详情" width="720px" top="6vh">
      <div v-if="detailRow">
        <p><strong>时间：</strong>{{ formatDate(detailRow.at) }}</p>
        <p><strong>操作者：</strong>{{ detailRow.actor }}</p>
        <p><strong>动作：</strong>{{ detailRow.action }}</p>
        <p><strong>对象：</strong>{{ detailRow.target }}</p>
        <p><strong>IP：</strong>{{ detailRow.ip }}</p>
        <el-divider />
        <el-row :gutter="16">
          <el-col :span="12">
            <div style="font-weight: 600; margin-bottom: 8px">请求</div>
            <pre class="psp-json">{{ formatJSON(detailRow.before_json) }}</pre>
          </el-col>
          <el-col :span="12">
            <div style="font-weight: 600; margin-bottom: 8px">结果</div>
            <pre class="psp-json">{{ formatJSON(detailRow.after_json) }}</pre>
          </el-col>
        </el-row>
      </div>
    </el-dialog>
  </div>
</template>

<style scoped>
.psp-json {
  font-family: ui-monospace, 'SFMono-Regular', Menlo, Consolas, monospace;
  font-size: 12px;
  line-height: 1.55;
  background: var(--code-bg);
  color: var(--text-main);
  border: 1px solid var(--code-border);
  padding: 10px;
  border-radius: 8px;
  max-height: 240px;
  overflow: auto;
  white-space: pre;
  word-break: normal;
}
</style>
