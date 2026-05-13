<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Delete, Lock, Unlock } from '@element-plus/icons-vue'
import {
  createUser,
  deleteUser,
  listUsers,
  resetCredentials,
  setEnabled,
  updateUser,
} from '@/api/users'
import { listGroups } from '@/api/groups'
import { runReconcile } from '@/api/reconcile'
import { useAuthStore } from '@/stores/auth'
import type { Group, User } from '@/api/types'

const auth = useAuthStore()

const users = ref<User[]>([])
const groups = ref<Group[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(50)
const search = ref('')
const loading = ref(false)
const reconcileBusy = ref(false)
const selectedUsers = ref<User[]>([])
const batchBusy = ref<'enable' | 'disable' | 'delete' | ''>('')
const selectedCount = computed(() => selectedUsers.value.length)

const createDialog = ref(false)
const createBusy = ref(false)
const createForm = reactive({
  username: '',
  display_name: '',
  password: '',
  group_id: undefined as number | undefined,
  expire_days: 30,
  traffic_limit_gb: 0,
  traffic_reset_period: 'monthly' as 'never' | 'monthly' | 'quarterly',
  remark: '',
})

const resultDialog = ref(false)
const resultUser = ref<User | null>(null)
const resultPassword = ref('')

const editDialog = ref(false)
const editBusy = ref(false)
const editing = ref<User | null>(null)
// expireMode: 'date' = ISO date, 'permanent' = clear_expire
const editForm = reactive({
  display_name: '',
  group_id: undefined as number | undefined,
  expireMode: 'date' as 'date' | 'permanent',
  expire_at: '' as string,
  traffic_limit_gb: 0,
  traffic_reset_period: 'monthly' as 'never' | 'monthly' | 'quarterly',
  remark: '',
})

async function load() {
  loading.value = true
  try {
    const res = await listUsers({
      page: page.value,
      page_size: pageSize.value,
      search: search.value,
    })
    users.value = res.items
    total.value = res.total
    selectedUsers.value = []
  } finally {
    loading.value = false
  }
}

function handleSelectionChange(rows: User[]) {
  selectedUsers.value = rows
}

function canSelectUser(row: User) {
  return row.auto_disabled_reason !== 'pending_delete'
}

async function loadGroups() {
  const res = await listGroups()
  groups.value = res.items
  if (!createForm.group_id && groups.value.length > 0) {
    createForm.group_id = groups.value[0].id
  }
}

function openCreate() {
  createForm.username = ''
  createForm.display_name = ''
  createForm.password = ''
  createForm.remark = ''
  createForm.expire_days = 30
  createForm.traffic_limit_gb = 0
  createForm.traffic_reset_period = 'monthly'
  createDialog.value = true
}

async function submitCreate() {
  if (!createForm.username) {
    ElMessage.warning('请填写用户名')
    return
  }
  if (!createForm.group_id) {
    ElMessage.warning('请选择分组')
    return
  }
  createBusy.value = true
  try {
    const expireAt =
      createForm.expire_days > 0
        ? new Date(Date.now() + createForm.expire_days * 86400000).toISOString()
        : undefined
    const res = await createUser({
      username: createForm.username,
      display_name: createForm.display_name || undefined,
      password: createForm.password || undefined,
      group_id: createForm.group_id,
      expire_at: expireAt,
      traffic_limit_gb: createForm.traffic_limit_gb,
      traffic_reset_period: createForm.traffic_reset_period,
      remark: createForm.remark || undefined,
    })
    resultUser.value = res.user
    resultPassword.value = res.initial_password
    createDialog.value = false
    resultDialog.value = true
    await load()
  } finally {
    createBusy.value = false
  }
}

function openEdit(row: User) {
  editing.value = row
  editForm.group_id = row.group_id
  if (row.expire_at) {
    editForm.expireMode = 'date'
    editForm.expire_at = row.expire_at
  } else {
    editForm.expireMode = 'permanent'
    editForm.expire_at = ''
  }
  editForm.traffic_limit_gb = Math.round(row.traffic_limit_bytes / 1024 / 1024 / 1024)
  editForm.traffic_reset_period = row.traffic_reset_period
  editForm.remark = row.remark ?? ''
  editForm.display_name = row.display_name ?? ''
  editDialog.value = true
}

async function submitEdit() {
  if (!editing.value) return
  if (!editForm.group_id) {
    ElMessage.warning('请选择分组')
    return
  }
  editBusy.value = true
  try {
    const req: Record<string, unknown> = {
      group_id: editForm.group_id,
      traffic_limit_gb: editForm.traffic_limit_gb,
      traffic_reset_period: editForm.traffic_reset_period,
      remark: editForm.remark,
      display_name: editForm.display_name,
    }
    if (editForm.expireMode === 'permanent') {
      req.clear_expire = true
    } else if (editForm.expire_at) {
      req.expire_at = editForm.expire_at
    }
    await updateUser(editing.value.id, req)
    // If the admin just edited their own row, propagate the new display
    // name into the auth store so the top-bar label updates immediately
    // instead of waiting for the next login.
    if (editing.value.id === auth.userId) {
      auth.setDisplayName(editForm.display_name || '')
    }
    ElMessage.success('已保存')
    editDialog.value = false
    await load()
  } finally {
    editBusy.value = false
  }
}

async function confirmResetCredentials(row: User) {
  try {
    await ElMessageBox.confirm(
      `重置凭证会导致 ${row.username} 的旧订阅链接立即失效，且现有连接被强制断开（需更新订阅获取新节点配置）。继续？`,
      '重置凭证',
      { type: 'warning' },
    )
    const res = await resetCredentials(row.id)
    ElMessage.success(`成功重置！新 UUID: ${res.uuid} | 新订阅: ${res.sub_url}`)
    await load()
  } catch (e: any) {
    if (e !== 'cancel') ElMessage.error('操作失败: ' + e)
  }
}

async function confirmDelete(row: User) {
  try {
    await ElMessageBox.confirm(
      `确定删除用户 ${row.username}？该操作将从所有 3X-UI inbound 清除其 client。`,
      '确认删除',
      { type: 'warning' },
    )
  } catch {
    return
  }
  await deleteUser(row.id)
  // Optimistically remove the row — actual DB deletion is async (background sync task).
  users.value = users.value.filter((u) => u.id !== row.id)
  total.value = Math.max(0, total.value - 1)
  ElMessage.success('已删除')
}

async function toggleEnabled(row: User) {
  await setEnabled(row.id, !row.enabled)
  ElMessage.success(!row.enabled ? '已启用' : '已禁用')
  await load()
}

async function batchSetEnabled(enabled: boolean) {
  if (selectedUsers.value.length === 0) return
  const rows = selectedUsers.value.slice()
  batchBusy.value = enabled ? 'enable' : 'disable'
  try {
    const results = await Promise.allSettled(rows.map((row) => setEnabled(row.id, enabled)))
    const failed = results.filter((result) => result.status === 'rejected').length
    if (failed > 0) {
      ElMessage.warning(`已${enabled ? '启用' : '禁用'} ${rows.length - failed} 个用户，失败 ${failed} 个`)
    } else {
      ElMessage.success(`已${enabled ? '启用' : '禁用'} ${rows.length} 个用户`)
    }
    await load()
  } finally {
    batchBusy.value = ''
  }
}

async function batchDelete() {
  if (selectedUsers.value.length === 0) return
  const rows = selectedUsers.value.slice()
  const names = rows
    .slice(0, 5)
    .map((row) => row.display_name || row.username)
    .join('、')
  const suffix = rows.length > 5 ? ` 等 ${rows.length} 个用户` : ''
  try {
    await ElMessageBox.confirm(
      `确定删除 ${names}${suffix}？该操作将从所有 3X-UI inbound 清除这些用户的 client。`,
      '批量删除用户',
      { type: 'warning' },
    )
  } catch {
    return
  }

  batchBusy.value = 'delete'
  try {
    const results = await Promise.allSettled(rows.map((row) => deleteUser(row.id)))
    const deletedRows = rows.filter((_, index) => results[index].status === 'fulfilled')
    const failed = rows.length - deletedRows.length
    users.value = users.value.filter((user) => !deletedRows.some((row) => row.id === user.id))
    total.value = Math.max(0, total.value - deletedRows.length)
    selectedUsers.value = []
    if (failed > 0) {
      ElMessage.warning(`已删除 ${deletedRows.length} 个用户，失败 ${failed} 个`)
    } else {
      ElMessage.success(`已删除 ${deletedRows.length} 个用户`)
    }
  } finally {
    batchBusy.value = ''
  }
}



function copyText(text: string) {
  navigator.clipboard.writeText(text)
  ElMessage.success('已复制')
}

function groupName(id: number): string {
  return groups.value.find((g) => g.id === id)?.name ?? String(id)
}

async function triggerReconcile() {
  reconcileBusy.value = true
  try {
    const report = await runReconcile()
    let msg = `巡检完成！共扫描 ${report.scanned} 个节点/用户。`
    if (report.fixed > 0) {
      msg += ` 自动修复了 ${report.fixed} 个问题。`
    } else {
      msg += ` 所有数据均一致。`
    }
    
    if (report.issues && report.issues.length > 0) {
      const issueDetails = report.issues.map((i: any) => `[${i.panel_name}] ${i.client_email}: ${i.detail}`)
      msg += `<br/><br/><strong>未修复问题：</strong><br/>` + issueDetails.join('<br/>')
      ElMessageBox.alert(msg, '巡检结果', { dangerouslyUseHTMLString: true, type: 'warning' })
    } else {
      ElMessage.success(msg)
    }
    await load()
  } catch (err: any) {
    ElMessage.error('执行失败: ' + err)
  } finally {
    reconcileBusy.value = false
  }
}

onMounted(async () => {
  await loadGroups()
  await load()
})
</script>

<template>
  <div class="psp-page">
    <div class="psp-page-header">
      <div class="psp-page-title">用户管理</div>
      <el-button type="primary" @click="openCreate">新增用户</el-button>
    </div>

    <div class="psp-toolbar">
      <el-input
        v-model="search"
        placeholder="搜索用户名 / UPN / 备注"
        style="width: 280px"
        clearable
        @change="load"
      />
      <el-button @click="load">刷新</el-button>
      <el-button 
        type="warning" 
        plain 
        :loading="reconcileBusy" 
        @click="triggerReconcile"
      >
        一键同步 (Reconcile)
      </el-button>
      <template v-if="selectedCount > 0">
        <el-divider direction="vertical" />
        <span class="users-selection-count">已选 {{ selectedCount }}</span>
        <el-button
          :icon="Unlock"
          :loading="batchBusy === 'enable'"
          :disabled="batchBusy !== ''"
          @click="batchSetEnabled(true)"
        >
          批量启用
        </el-button>
        <el-button
          :icon="Lock"
          :loading="batchBusy === 'disable'"
          :disabled="batchBusy !== ''"
          @click="batchSetEnabled(false)"
        >
          批量禁用
        </el-button>
        <el-button
          type="danger"
          :icon="Delete"
          :loading="batchBusy === 'delete'"
          :disabled="batchBusy !== ''"
          @click="batchDelete"
        >
          批量删除
        </el-button>
      </template>
    </div>

    <el-table v-loading="loading" :data="users" stripe @selection-change="handleSelectionChange">
      <el-table-column type="selection" width="48" :selectable="canSelectUser" />
      <el-table-column label="显示名" min-width="160">
        <template #default="{ row }">{{ row.display_name || row.username }}</template>
      </el-table-column>
      <el-table-column prop="username" label="登录名" min-width="160" />
      <el-table-column label="分组" min-width="140">
        <template #default="{ row }">{{ groupName(row.group_id) }}</template>
      </el-table-column>
      <el-table-column label="到期" min-width="160">
        <template #default="{ row }">
          {{ row.expire_at ? new Date(row.expire_at).toLocaleDateString() : '永久' }}
        </template>
      </el-table-column>
      <el-table-column label="流量限额" width="120">
        <template #default="{ row }">
          {{
            row.traffic_limit_bytes > 0
              ? (row.traffic_limit_bytes / 1024 / 1024 / 1024).toFixed(0) + ' GB'
              : '不限'
          }}
        </template>
      </el-table-column>
      <el-table-column label="状态" width="120">
        <template #default="{ row }">
          <el-tag v-if="row.enabled" type="success" size="small">已启用</el-tag>
          <el-tag v-else-if="row.auto_disabled_reason === 'pending_approval'" type="warning" size="small">待审批</el-tag>
          <el-tag v-else-if="row.auto_disabled_reason === 'pending_delete'" type="info" size="small">删除中</el-tag>
          <el-tag v-else type="danger" size="small">
            {{ row.auto_disabled_reason === 'traffic_exceeded' ? '超流量' : '已禁用' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="订阅 URL" min-width="200">
        <template #default="{ row }">
          <el-button text size="small" @click="copyText(row.sub_url)">复制</el-button>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="380">
        <template #default="{ row }">
          <el-button size="small" type="primary" @click="openEdit(row)">编辑</el-button>
          <el-button size="small" @click="toggleEnabled(row)">
            {{ row.enabled ? '禁用' : '启用' }}
          </el-button>
          <el-button size="small" @click="confirmResetCredentials(row)">重置凭证</el-button>
          <el-button size="small" type="danger" @click="confirmDelete(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-pagination
      v-model:current-page="page"
      v-model:page-size="pageSize"
      :total="total"
      :page-sizes="[20, 50, 100]"
      layout="total, sizes, prev, pager, next"
      style="margin-top: 16px"
      @current-change="load"
      @size-change="load"
    />

    <!-- Create user dialog -->
    <el-dialog v-model="createDialog" title="新增用户" width="500px">
      <el-form label-width="100px" :model="createForm">
        <el-form-item label="登录名" required>
          <el-input v-model="createForm.username" placeholder="登录用的标识，例如邮箱或自定义 id" />
        </el-form-item>
        <el-form-item label="显示名">
          <el-input v-model="createForm.display_name" placeholder="UI 上显示的友好名称（可留空，将回退到登录名）" />
        </el-form-item>
        <el-form-item label="初始密码">
          <el-input
            v-model="createForm.password"
            placeholder="留空自动生成"
            show-password
          />
        </el-form-item>
        <el-form-item label="分组" required>
          <el-select v-model="createForm.group_id" placeholder="选择分组" style="width: 100%">
            <el-option
              v-for="g in groups"
              :key="g.id"
              :label="g.name"
              :value="g.id"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="有效期（天）">
          <el-input-number v-model="createForm.expire_days" :min="0" />
          <span style="margin-left: 8px; color: var(--text-muted)">0 = 永久</span>
        </el-form-item>
        <el-form-item label="流量限额 (GB)">
          <el-input-number v-model="createForm.traffic_limit_gb" :min="0" />
          <span style="margin-left: 8px; color: var(--text-muted)">0 = 不限</span>
        </el-form-item>
        <el-form-item label="重置周期">
          <el-select v-model="createForm.traffic_reset_period" style="width: 100%">
            <el-option label="不重置" value="never" />
            <el-option label="月度" value="monthly" />
            <el-option label="季度" value="quarterly" />
          </el-select>
        </el-form-item>
        <el-form-item label="备注">
          <el-input v-model="createForm.remark" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="createDialog = false">取消</el-button>
        <el-button type="primary" :loading="createBusy" @click="submitCreate">创建</el-button>
      </template>
    </el-dialog>

    <!-- Edit user dialog -->
    <el-dialog v-model="editDialog" title="编辑用户" width="540px">
      <div v-if="editing">
        <!-- Read-only info -->
        <el-descriptions :column="1" border size="small" style="margin-bottom: 16px">
          <el-descriptions-item label="用户名">{{ editing.username }}</el-descriptions-item>
          <el-descriptions-item label="UPN" v-if="editing.upn">{{ editing.upn }}</el-descriptions-item>
          <el-descriptions-item label="UUID">
            <code style="font-size: 12px">{{ editing.uuid }}</code>
          </el-descriptions-item>
          <el-descriptions-item label="订阅 URL">
            <el-button text size="small" @click="copyText(editing.sub_url)">复制 URL</el-button>
          </el-descriptions-item>
          <el-descriptions-item label="创建时间">
            {{ new Date(editing.created_at).toLocaleString() }}
          </el-descriptions-item>
        </el-descriptions>

        <!-- Editable fields -->
        <el-form label-width="100px" :model="editForm">
          <el-form-item label="显示名">
            <el-input v-model="editForm.display_name" placeholder="UI 上显示的友好名称（可留空，将回退到登录名）" />
          </el-form-item>
          <el-form-item label="分组" required>
            <el-select v-model="editForm.group_id" style="width: 100%">
              <el-option v-for="g in groups" :key="g.id" :label="g.name" :value="g.id" />
            </el-select>
          </el-form-item>
          <el-form-item label="到期">
            <el-radio-group v-model="editForm.expireMode" style="margin-bottom: 8px">
              <el-radio value="date">指定日期</el-radio>
              <el-radio value="permanent">永久</el-radio>
            </el-radio-group>
            <el-date-picker
              v-if="editForm.expireMode === 'date'"
              v-model="editForm.expire_at"
              type="datetime"
              placeholder="选择到期时间"
              style="width: 100%"
              value-format="YYYY-MM-DDTHH:mm:ss[Z]"
            />
          </el-form-item>
          <el-form-item label="流量限额 (GB)">
            <el-input-number v-model="editForm.traffic_limit_gb" :min="0" />
            <span style="margin-left: 8px; color: var(--text-muted)">0 = 不限</span>
          </el-form-item>
          <el-form-item label="重置周期">
            <el-select v-model="editForm.traffic_reset_period" style="width: 100%">
              <el-option label="不重置" value="never" />
              <el-option label="月度" value="monthly" />
              <el-option label="季度" value="quarterly" />
            </el-select>
          </el-form-item>
          <el-form-item label="备注">
            <el-input v-model="editForm.remark" />
          </el-form-item>
        </el-form>
      </div>
      <template #footer>
        <el-button @click="editDialog = false">取消</el-button>
        <el-button type="primary" :loading="editBusy" @click="submitEdit">保存</el-button>
      </template>
    </el-dialog>

    <!-- Result dialog (showing initial password and sub URL) -->
    <el-dialog v-model="resultDialog" title="创建成功" width="500px">
      <div v-if="resultUser">
        <p>
          用户 <strong>{{ resultUser.username }}</strong> 已创建。请将以下信息发给朋友：
        </p>
        <el-form label-width="100px">
          <el-form-item label="初始密码">
            <el-input v-model="resultPassword" readonly>
              <template #append>
                <el-button @click="copyText(resultPassword)">复制</el-button>
              </template>
            </el-input>
            <div style="color: #e6a23c; font-size: 12px; margin-top: 4px">
              此密码仅显示一次，请立即保存
            </div>
          </el-form-item>
          <el-form-item label="订阅 URL">
            <el-input :model-value="resultUser.sub_url" readonly>
              <template #append>
                <el-button @click="copyText(resultUser.sub_url)">复制</el-button>
              </template>
            </el-input>
          </el-form-item>
        </el-form>
      </div>
      <template #footer>
        <el-button type="primary" @click="resultDialog = false">完成</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.users-selection-count {
  color: var(--text-muted);
  white-space: nowrap;
}
</style>
