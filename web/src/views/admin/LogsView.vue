<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { getUISettings, putUISettings } from '@/api/settings'
import { getSubLogs, clearSubLogs, purgeSubLogs, type SubLog } from '@/api/subLogs'
import { listAudit, clearAudit, type AuditEntry } from '@/api/audit'

const activeTab = ref('sub')
const retentionDays = ref(7)
const savingRetention = ref(false)

// ---- Subscription Logs ----
const subItems = ref<SubLog[]>([])
const subTotal = ref(0)
const subPage = ref(1)
const subPageSize = ref(50)
const subLoading = ref(false)
const subClearing = ref(false)
const subDetailDialog = ref(false)
const subDetailRow = ref<SubLog | null>(null)

async function loadSubLogs() {
  subLoading.value = true
  try {
    const res = await getSubLogs({
      page: subPage.value,
      page_size: subPageSize.value,
    })
    subItems.value = res.items
    subTotal.value = res.total
  } finally {
    subLoading.value = false
  }
}

async function clearSubAll() {
  await ElMessageBox.confirm('确定清空所有订阅日志？此操作不可恢复。', '清空订阅日志', {
    type: 'warning',
    confirmButtonText: '清空',
    cancelButtonText: '取消',
  })
  subClearing.value = true
  try {
    await clearSubLogs()
    ElMessage.success('已清空')
    await loadSubLogs()
  } finally {
    subClearing.value = false
  }
}

async function purgeSubOld() {
  try {
    const res = await purgeSubLogs()
    ElMessage.success(`已清理 ${res.deleted} 条过期日志`)
    await loadSubLogs()
  } catch {
    // error handled by interceptor
  }
}

function formatSubDate(value?: string) {
  if (!value) return '-'
  const d = new Date(value)
  return Number.isNaN(d.getTime()) ? '-' : d.toLocaleString()
}

function showSubDetail(row: SubLog) {
  subDetailRow.value = row
  subDetailDialog.value = true
}

// ---- Audit Logs ----
const auditItems = ref<AuditEntry[]>([])
const auditTotal = ref(0)
const auditPage = ref(1)
const auditPageSize = ref(50)
const auditLoading = ref(false)
const auditClearing = ref(false)
const actorFilter = ref('')
const actionFilter = ref('')

const detailDialog = ref(false)
const detailRow = ref<AuditEntry | null>(null)

async function loadAuditLogs() {
  auditLoading.value = true
  try {
    const res = await listAudit({
      page: auditPage.value,
      page_size: auditPageSize.value,
      actor: actorFilter.value || undefined,
      action: actionFilter.value || undefined,
    })
    auditItems.value = res.items
    auditTotal.value = res.total
  } finally {
    auditLoading.value = false
  }
}

async function clearAuditAll() {
  await ElMessageBox.confirm('确定清空所有审计日志？此操作不可恢复。', '清空审计日志', {
    type: 'warning',
    confirmButtonText: '清空',
    cancelButtonText: '取消',
  })
  auditClearing.value = true
  try {
    await clearAudit()
    ElMessage.success('已清空')
    await loadAuditLogs()
  } finally {
    auditClearing.value = false
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

function formatAuditDate(value?: string) {
  if (!value) return '-'
  const d = new Date(value)
  return Number.isNaN(d.getTime()) ? '-' : d.toLocaleString()
}

// ---- Retention Settings ----
async function loadRetention() {
  try {
    const s = await getUISettings()
    retentionDays.value = s.sub_log_retention_days || 7
  } catch {
    // error handled by interceptor
  }
}

async function saveRetention() {
  savingRetention.value = true
  try {
    const s = await getUISettings()
    s.sub_log_retention_days = retentionDays.value
    await putUISettings(s)
    ElMessage.success('保留天数已保存')
  } finally {
    savingRetention.value = false
  }
}

// ---- Tab Change ----
watch(activeTab, (tab) => {
  if (tab === 'sub') loadSubLogs()
  if (tab === 'audit') loadAuditLogs()
})

onMounted(() => {
  loadSubLogs()
  loadRetention()
})
</script>

<template>
  <div class="psp-page">
    <div class="psp-page-header">
      <div class="psp-page-title">日志管理</div>
      <div class="header-actions">
        <span class="retention-label">保留天数:</span>
        <el-input-number
          v-model="retentionDays"
          :min="0"
          :max="365"
          size="small"
          style="width: 100px"
        />
        <el-button size="small" :loading="savingRetention" @click="saveRetention">保存</el-button>
      </div>
    </div>

    <el-tabs v-model="activeTab">
      <!-- Subscription Logs -->
      <el-tab-pane label="订阅日志" name="sub">
        <div class="tab-toolbar">
          <el-button @click="loadSubLogs">刷新</el-button>
          <el-button type="warning" plain @click="purgeSubOld">清理过期</el-button>
          <el-button type="danger" plain :loading="subClearing" @click="clearSubAll">清空所有</el-button>
        </div>

        <el-table v-loading="subLoading" :data="subItems" stripe>
          <el-table-column label="时间" min-width="180">
            <template #default="{ row }">{{ formatSubDate(row.accessed_at) }}</template>
          </el-table-column>
          <el-table-column prop="user_upn" label="用户" min-width="150">
            <template #default="{ row }">{{ row.user_upn || `ID: ${row.user_id}` }}</template>
          </el-table-column>
          <el-table-column prop="ip" label="IP" width="140" />
          <el-table-column prop="client_type" label="客户端" width="120">
            <template #default="{ row }">
              <el-tag size="small">{{ row.client_type || '-' }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="ua" label="User-Agent" min-width="200" show-overflow-tooltip />
          <el-table-column label="详情" width="80" align="center">
            <template #default="{ row }">
              <el-button size="small" @click="showSubDetail(row)">查看</el-button>
            </template>
          </el-table-column>
        </el-table>

        <el-pagination
          v-model:current-page="subPage"
          v-model:page-size="subPageSize"
          :total="subTotal"
          :page-sizes="[20, 50, 100, 200]"
          layout="total, sizes, prev, pager, next"
          style="margin-top: 16px"
          @current-change="loadSubLogs"
          @size-change="loadSubLogs"
        />
      </el-tab-pane>

      <!-- Audit Logs -->
      <el-tab-pane label="审计日志" name="audit">
        <div class="tab-toolbar">
          <el-input
            v-model="actorFilter"
            placeholder="按操作者筛选"
            style="width: 200px"
            clearable
            @change="loadAuditLogs"
          />
          <el-input
            v-model="actionFilter"
            placeholder="按动作筛选"
            style="width: 240px"
            clearable
            @change="loadAuditLogs"
          />
          <el-button @click="loadAuditLogs">刷新</el-button>
          <el-button type="danger" plain :loading="auditClearing" @click="clearAuditAll">清空所有</el-button>
        </div>

        <el-table v-loading="auditLoading" :data="auditItems" stripe>
          <el-table-column label="时间" min-width="180">
            <template #default="{ row }">{{ formatAuditDate(row.at) }}</template>
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
          v-model:current-page="auditPage"
          v-model:page-size="auditPageSize"
          :total="auditTotal"
          :page-sizes="[20, 50, 100, 200]"
          layout="total, sizes, prev, pager, next"
          style="margin-top: 16px"
          @current-change="loadAuditLogs"
          @size-change="loadAuditLogs"
        />
      </el-tab-pane>
    </el-tabs>

    <!-- Subscription Log Detail Dialog -->
    <el-dialog v-model="subDetailDialog" title="订阅日志详情" width="560px" top="10vh">
      <div v-if="subDetailRow">
        <el-descriptions :column="1" border>
          <el-descriptions-item label="时间">{{ formatSubDate(subDetailRow.accessed_at) }}</el-descriptions-item>
          <el-descriptions-item label="用户">
            {{ subDetailRow.user_upn || '-' }}
            <span v-if="subDetailRow.user_display" style="color: var(--text-muted); margin-left: 8px;">
              ({{ subDetailRow.user_display }})
            </span>
          </el-descriptions-item>
          <el-descriptions-item label="用户ID">{{ subDetailRow.user_id }}</el-descriptions-item>
          <el-descriptions-item label="IP">{{ subDetailRow.ip }}</el-descriptions-item>
          <el-descriptions-item label="客户端类型">
            <el-tag size="small">{{ subDetailRow.client_type || '-' }}</el-tag>
          </el-descriptions-item>
          <el-descriptions-item label="User-Agent">
            <div style="word-break: break-all;">{{ subDetailRow.ua || '-' }}</div>
          </el-descriptions-item>
        </el-descriptions>
      </div>
    </el-dialog>

    <!-- Audit Detail Dialog -->
    <el-dialog v-model="detailDialog" title="审计详情" width="720px" top="6vh">
      <div v-if="detailRow">
        <p><strong>时间：</strong>{{ formatAuditDate(detailRow.at) }}</p>
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
.psp-page-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 20px;
}

.header-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.retention-label {
  font-size: 14px;
  color: var(--text-muted);
}

.tab-toolbar {
  display: flex;
  gap: 8px;
  margin-bottom: 16px;
}

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

@media (max-width: 768px) {
  .psp-page-header {
    flex-direction: column;
    align-items: flex-start;
    gap: 12px;
  }

  .header-actions {
    flex-wrap: wrap;
  }

  .tab-toolbar {
    flex-wrap: wrap;
  }

  .el-table {
    font-size: 13px;
  }
}
</style>
