<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  createUser,
  deleteUser,
  listUsers,
  resetSubToken,
  setEnabled,
} from '@/api/users'
import { listGroups } from '@/api/groups'
import type { Group, User } from '@/api/types'

const users = ref<User[]>([])
const groups = ref<Group[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(50)
const search = ref('')
const loading = ref(false)

const createDialog = ref(false)
const createBusy = ref(false)
const createForm = reactive({
  username: '',
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
  } finally {
    loading.value = false
  }
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

async function confirmDelete(row: User) {
  await ElMessageBox.confirm(
    `确定删除用户 ${row.username}？该操作将从所有 3X-UI inbound 清除其 client。`,
    '确认删除',
    { type: 'warning' },
  )
  await deleteUser(row.id)
  ElMessage.success('已删除')
  await load()
}

async function toggleEnabled(row: User) {
  await setEnabled(row.id, !row.enabled)
  ElMessage.success(!row.enabled ? '已启用' : '已禁用')
  await load()
}

async function resetToken(row: User) {
  await ElMessageBox.confirm(
    `重置后旧订阅 URL 立即失效，确定？`,
    '重置 sub_token',
    { type: 'warning' },
  )
  const res = await resetSubToken(row.id)
  ElMessage.success('新订阅 URL：' + res.sub_url)
  await load()
}

function copyText(text: string) {
  navigator.clipboard.writeText(text)
  ElMessage.success('已复制')
}

function groupName(id: number): string {
  return groups.value.find((g) => g.id === id)?.name ?? String(id)
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
    </div>

    <el-table v-loading="loading" :data="users" stripe>
      <el-table-column prop="username" label="用户名" min-width="160" />
      <el-table-column label="来源" width="80">
        <template #default="{ row }">
          <el-tag :type="row.source === 'sso' ? 'success' : 'info'" size="small">
            {{ row.source === 'sso' ? 'SSO' : '本地' }}
          </el-tag>
        </template>
      </el-table-column>
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
          <el-tag v-else type="danger" size="small">
            {{
              row.auto_disabled_reason === 'traffic_exceeded' ? '超流量' : '已禁用'
            }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="订阅 URL" min-width="200">
        <template #default="{ row }">
          <el-button text size="small" @click="copyText(row.sub_url)">复制</el-button>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="280" fixed="right">
        <template #default="{ row }">
          <el-button size="small" @click="toggleEnabled(row)">
            {{ row.enabled ? '禁用' : '启用' }}
          </el-button>
          <el-button size="small" @click="resetToken(row)">重置 token</el-button>
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
        <el-form-item label="用户名" required>
          <el-input v-model="createForm.username" placeholder="local_xxx" />
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
          <span style="margin-left: 8px; color: #909399">0 = 永久</span>
        </el-form-item>
        <el-form-item label="流量限额 (GB)">
          <el-input-number v-model="createForm.traffic_limit_gb" :min="0" />
          <span style="margin-left: 8px; color: #909399">0 = 不限</span>
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
