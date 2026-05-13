<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Clock, Delete, Lock, Unlock } from '@element-plus/icons-vue'
import {
  createUser,
  deleteUser,
  getUserRules,
  listUsers,
  resetCredentials,
  resetEmergencyUsage,
  setEnabled,
  updateUser,
  updateUserRules,
} from '@/api/users'
import { listGroups } from '@/api/groups'
import { runReconcile } from '@/api/reconcile'
import { setUserTraffic, userTraffic } from '@/api/traffic'
import { useAuthStore } from '@/stores/auth'
import type { Group, Role, User } from '@/api/types'

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
const batchBusy = ref<'enable' | 'disable' | 'delete' | 'resetEmergency' | 'renew' | ''>('')
const selectedCount = computed(() => selectedUsers.value.length)
const renewableSelectedCount = computed(() => selectedUsers.value.filter((row) => canQuickRenew(row)).length)

const createDialog = ref(false)
const createBusy = ref(false)
const createForm = reactive({
  upn: '',
  email: '',
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
const editTrafficLoading = ref(false)
const editOriginalUsedTrafficGB = ref(0)
const editing = ref<User | null>(null)
const rulesDialog = ref(false)
const rulesUser = ref<User | null>(null)
const rulesText = ref('')
const rulesSaved = ref('')
const rulesBusy = ref(false)
const rulesDirty = computed(() => rulesText.value.trim() !== rulesSaved.value.trim())
// expireMode: 'date' = ISO date, 'permanent' = clear_expire
const editForm = reactive({
  display_name: '',
  email: '',
  group_id: undefined as number | undefined,
  role: 'user' as Role,
  expireMode: 'date' as 'date' | 'permanent',
  expire_at: '' as string,
  traffic_limit_gb: 0,
  used_traffic_gb: 0,
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
  createForm.upn = ''
  createForm.email = ''
  createForm.display_name = ''
  createForm.password = ''
  createForm.remark = ''
  createForm.expire_days = 30
  createForm.traffic_limit_gb = 0
  createForm.traffic_reset_period = 'monthly'
  createDialog.value = true
}

async function submitCreate() {
  if (!createForm.upn) {
    ElMessage.warning('请填写 UPN')
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
      upn: createForm.upn,
      email: createForm.email || undefined,
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

function bytesToGB(bytes: number) {
  return Math.round((bytes / 1024 / 1024 / 1024) * 100) / 100
}

async function openEdit(row: User) {
  editing.value = row
  editForm.group_id = row.group_id
  editForm.role = row.role
  editForm.email = row.email ?? ''
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
  editForm.used_traffic_gb = 0
  editOriginalUsedTrafficGB.value = 0
  editDialog.value = true
  editTrafficLoading.value = true
  try {
    const report = await userTraffic(row.id)
    editForm.used_traffic_gb = bytesToGB(report.period_used_bytes)
    editOriginalUsedTrafficGB.value = editForm.used_traffic_gb
  } finally {
    editTrafficLoading.value = false
  }
}

async function submitEdit() {
  if (!editing.value) return
  if (!editForm.group_id) {
    ElMessage.warning('请选择分组')
    return
  }
  editBusy.value = true
  try {
    if (editing.value.id === auth.userId && editing.value.role === 'admin' && editForm.role === 'user') {
      try {
        await ElMessageBox.confirm(
          '你正在把当前登录的管理员账号降级为普通用户，保存后会失去管理后台权限。继续？',
          '确认降级',
          { type: 'warning' },
        )
      } catch {
        return
      }
    }
    const req: Record<string, unknown> = {
      group_id: editForm.group_id,
      email: editForm.email,
      traffic_limit_gb: editForm.traffic_limit_gb,
      traffic_reset_period: editForm.traffic_reset_period,
      remark: editForm.remark,
      display_name: editForm.display_name,
      role: editForm.role,
    }
    if (editForm.expireMode === 'permanent') {
      req.clear_expire = true
    } else if (editForm.expire_at) {
      req.expire_at = editForm.expire_at
    }
    await updateUser(editing.value.id, req)
    if (editForm.used_traffic_gb !== editOriginalUsedTrafficGB.value) {
      await setUserTraffic(editing.value.id, editForm.used_traffic_gb)
    }
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
      `重置凭证会导致 ${row.upn} 的旧订阅链接立即失效，且现有连接被强制断开（需更新订阅获取新节点配置）。继续？`,
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

async function confirmResetEmergencyUsage(row: User) {
  await resetEmergencyUsage(row.id)
  ElMessage.success('已重置紧急使用次数')
  await load()
}

async function confirmDelete(row: User) {
  try {
    await ElMessageBox.confirm(
      `确定删除用户 ${row.upn}？该操作将从所有 3X-UI inbound 清除其 client。`,
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
  if (editing.value?.id === row.id) {
    editDialog.value = false
  }
  ElMessage.success('已删除')
}

async function toggleEnabled(row: User) {
  const enabling = !row.enabled
  let reason = ''

  // Ask for reason (optional for both enable/disable)
  try {
    const action = enabling ? '启用' : '停用'
    const { value } = await ElMessageBox.prompt(`请输入${action}原因（可选）`, `${action}用户`, {
      confirmButtonText: '确定',
      cancelButtonText: '取消',
      inputPlaceholder: `${action}原因`,
      inputType: 'textarea',
    })
    reason = value || ''
  } catch {
    return // User cancelled
  }

  await setEnabled(row.id, enabling, reason)
  ElMessage.success(enabling ? '已启用' : '已禁用')
  await load()
}

function canQuickRenew(row: User) {
  return !!row.expire_at && row.auto_disabled_reason !== 'pending_delete'
}

function renewedExpireAt(row: User, days = 30) {
  const now = Date.now()
  const current = row.expire_at ? new Date(row.expire_at).getTime() : 0
  const base = Number.isFinite(current) && current > now ? current : now
  return new Date(base + days * 86400000).toISOString()
}

async function quickRenew(row: User, days = 30) {
  if (!canQuickRenew(row)) {
    ElMessage.info('永久账号无需快捷续期；如需改成限时账号，请进入编辑设置到期时间')
    return
  }
  await updateUser(row.id, { expire_at: renewedExpireAt(row, days) })
  ElMessage.success(`已续期 ${days} 天`)
  await load()
}

async function batchSetEnabled(enabled: boolean) {
  if (selectedUsers.value.length === 0) return
  const rows = selectedUsers.value.slice()

  // Ask for reason (optional)
  let reason = ''
  try {
    const action = enabled ? '启用' : '停用'
    const { value } = await ElMessageBox.prompt(`请输入${action}原因（可选）`, `批量${action}用户`, {
      confirmButtonText: '确定',
      cancelButtonText: '取消',
      inputPlaceholder: `${action}原因`,
      inputType: 'textarea',
    })
    reason = value || ''
  } catch {
    return // User cancelled
  }

  batchBusy.value = enabled ? 'enable' : 'disable'
  try {
    const results = await Promise.allSettled(rows.map((row) => setEnabled(row.id, enabled, reason)))
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

async function batchQuickRenew(days = 30) {
  if (selectedUsers.value.length === 0) return
  const rows = selectedUsers.value.filter((row) => canQuickRenew(row))
  if (rows.length === 0) {
    ElMessage.info('所选用户没有可快捷续期的限时账号')
    return
  }
  batchBusy.value = 'renew'
  try {
    const results = await Promise.allSettled(
      rows.map((row) => updateUser(row.id, { expire_at: renewedExpireAt(row, days) })),
    )
    const failed = results.filter((result) => result.status === 'rejected').length
    const skipped = selectedUsers.value.length - rows.length
    const skippedText = skipped > 0 ? `，跳过 ${skipped} 个永久/删除中账号` : ''
    if (failed > 0) {
      ElMessage.warning(`已续期 ${rows.length - failed} 个用户，失败 ${failed} 个${skippedText}`)
    } else {
      ElMessage.success(`已续期 ${rows.length} 个用户${skippedText}`)
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
    .map((row) => row.display_name || row.upn)
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

function batchMoreCommand(command: string) {
  if (command === 'resetEmergency') {
    batchResetEmergencyUsage()
  } else if (command === 'delete') {
    batchDelete()
  }
}

async function batchResetEmergencyUsage() {
  if (selectedUsers.value.length === 0) return
  const rows = selectedUsers.value.slice()
  batchBusy.value = 'resetEmergency'
  try {
    const results = await Promise.allSettled(rows.map((row) => resetEmergencyUsage(row.id)))
    const failed = results.filter((result) => result.status === 'rejected').length
    if (failed > 0) {
      ElMessage.warning(`已重置 ${rows.length - failed} 个用户，失败 ${failed} 个`)
    } else {
      ElMessage.success(`已重置 ${rows.length} 个用户的紧急使用次数`)
    }
    await load()
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

async function openRulesDialog(row: User) {
  rulesUser.value = row
  rulesBusy.value = true
  rulesDialog.value = true
  try {
    const rules = await getUserRules(row.id)
    rulesText.value = rules
    rulesSaved.value = rules
  } finally {
    rulesBusy.value = false
  }
}

function resetRulesEditor() {
  rulesText.value = rulesSaved.value
}

async function saveUserRules() {
  if (!rulesUser.value) return
  rulesBusy.value = true
  try {
    const rules = rulesText.value.trim()
    await updateUserRules(rulesUser.value.id, rules)
    rulesText.value = rules
    rulesSaved.value = rules
    ElMessage.success('个人规则已保存')
    rulesDialog.value = false
  } finally {
    rulesBusy.value = false
  }
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
        placeholder="搜索 UPN / 邮箱 / 备注"
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
          :icon="Clock"
          :loading="batchBusy === 'renew'"
          :disabled="batchBusy !== ''"
          @click="batchQuickRenew(30)"
        >
          续期30天
        </el-button>
        <el-dropdown :disabled="batchBusy !== ''" @command="batchMoreCommand">
          <el-button>
            更多
          </el-button>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item command="resetEmergency">重置紧急次数</el-dropdown-item>
              <el-dropdown-item command="delete" divided>批量删除</el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
        <span v-if="renewableSelectedCount < selectedCount" class="users-selection-hint">
          {{ selectedCount - renewableSelectedCount }} 个永久/删除中账号不会续期
        </span>
      </template>
    </div>

    <el-table v-loading="loading" :data="users" stripe @selection-change="handleSelectionChange">
      <el-table-column type="selection" width="48" :selectable="canSelectUser" />
      <el-table-column prop="id" label="UserID" min-width="100" />
      <el-table-column prop="upn" label="UPN" min-width="200" />
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
      <el-table-column label="紧急次数" width="100">
        <template #default="{ row }">{{ row.emergency_used_count || 0 }}</template>
      </el-table-column>
      <el-table-column label="订阅 URL" min-width="200">
        <template #default="{ row }">
          <el-button text size="small" @click="copyText(row.sub_url)">复制</el-button>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="300" fixed="right">
        <template #default="{ row }">
          <el-button size="small" type="primary" @click="openEdit(row)">编辑</el-button>
          <el-button size="small" :disabled="!canQuickRenew(row)" @click="quickRenew(row, 30)">
            续期30天
          </el-button>
          <el-button size="small" @click="toggleEnabled(row)">
            {{ row.enabled ? '禁用' : '启用' }}
          </el-button>
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
      <el-form class="user-dialog-form" label-width="132px" :model="createForm">
        <el-form-item label="UPN" required>
          <el-input v-model="createForm.upn" placeholder="本地登录和 SSO 统一标识，例如 me@example.com" />
        </el-form-item>
        <el-form-item label="显示名">
          <el-input v-model="createForm.display_name" placeholder="UI 上显示的友好名称（可留空，将回退到 UPN）" />
        </el-form-item>
        <el-form-item label="收件邮箱">
          <el-input v-model="createForm.email" placeholder="邮件提醒收件地址；可留空使用邮箱格式 UPN" />
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
        <el-form-item label="流量限额">
          <el-input-number v-model="createForm.traffic_limit_gb" :min="0" />
          <span class="input-suffix">GB，0 = 不限</span>
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
          <el-descriptions-item label="UPN">{{ editing.upn }}</el-descriptions-item>
          <el-descriptions-item label="收件邮箱" v-if="editing.email">{{ editing.email }}</el-descriptions-item>
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
        <el-form class="user-dialog-form" label-width="132px" :model="editForm">
          <el-form-item label="显示名">
            <el-input v-model="editForm.display_name" placeholder="UI 上显示的友好名称（可留空，将回退到 UPN）" />
          </el-form-item>
          <el-form-item label="收件邮箱">
            <el-input v-model="editForm.email" placeholder="邮件提醒收件地址；SSO 登录会用 Email claim 更新" />
          </el-form-item>
          <el-form-item label="权限">
            <el-radio-group v-model="editForm.role">
              <el-radio value="user">普通用户</el-radio>
              <el-radio value="admin">管理员</el-radio>
            </el-radio-group>
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
          <el-form-item label="流量限额">
            <el-input-number v-model="editForm.traffic_limit_gb" :min="0" />
            <span class="input-suffix">GB，0 = 不限</span>
          </el-form-item>
          <el-form-item label="已用流量">
            <el-input-number
              v-model="editForm.used_traffic_gb"
              :min="0"
              :precision="2"
              :step="1"
              :disabled="editTrafficLoading"
            />
            <span class="input-suffix">GB，本计费周期</span>
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

        <div class="edit-ops">
          <div class="edit-ops-title">管理操作</div>
          <div class="edit-ops-buttons">
            <el-button size="small" @click="copyText(editing.sub_url)">复制订阅</el-button>
            <el-button size="small" @click="openRulesDialog(editing)">个人规则</el-button>
            <el-button size="small" @click="confirmResetCredentials(editing)">重置凭证</el-button>
            <el-button size="small" @click="confirmResetEmergencyUsage(editing)">重置紧急次数</el-button>
            <el-button size="small" type="danger" :icon="Delete" @click="confirmDelete(editing)">删除用户</el-button>
          </div>
        </div>
      </div>
      <template #footer>
        <el-button @click="editDialog = false">取消</el-button>
        <el-button type="primary" :loading="editBusy" @click="submitEdit">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="rulesDialog" title="个人规则" width="720px" top="8vh">
      <div v-if="rulesUser" class="rules-user">UPN: {{ rulesUser.upn }}</div>
      <el-input
        v-model="rulesText"
        type="textarea"
        :rows="14"
        resize="vertical"
        placeholder="- DOMAIN-SUFFIX,example.com,DIRECT"
        class="rules-editor"
      />
      <p class="rules-hint">按 mihomo rules 格式填写，每行一条。只影响该用户的订阅渲染。</p>
      <template #footer>
        <el-button :disabled="!rulesDirty || rulesBusy" @click="resetRulesEditor">撤销</el-button>
        <el-button @click="rulesDialog = false">取消</el-button>
        <el-button type="primary" :disabled="!rulesDirty" :loading="rulesBusy" @click="saveUserRules">
          保存规则
        </el-button>
      </template>
    </el-dialog>

    <!-- Result dialog (showing initial password and sub URL) -->
    <el-dialog v-model="resultDialog" title="创建成功" width="500px">
      <div v-if="resultUser">
        <p>
          用户 <strong>{{ resultUser.upn }}</strong> 已创建。请将以下信息发给朋友：
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

.users-selection-hint {
  color: var(--text-muted);
  font-size: 12px;
  white-space: nowrap;
}

.user-dialog-form :deep(.el-form-item__label) {
  line-height: 32px;
  white-space: nowrap;
}

.input-suffix {
  color: var(--text-muted);
  font-size: 13px;
  margin-left: 10px;
  white-space: nowrap;
}

.edit-ops {
  border-top: 1px solid var(--header-border);
  margin-top: 18px;
  padding-top: 14px;
}

.edit-ops-title {
  color: var(--text-muted);
  font-size: 12px;
  margin-bottom: 10px;
}

.edit-ops-buttons {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.rules-user {
  color: var(--text-muted);
  font-size: 13px;
  margin-bottom: 10px;
}

.rules-editor :deep(textarea) {
  font-family: ui-monospace, 'SFMono-Regular', Menlo, Consolas, monospace;
  font-size: 13px;
  line-height: 1.5;
}

.rules-hint {
  color: var(--text-muted);
  font-size: 12px;
  line-height: 1.5;
}
</style>
