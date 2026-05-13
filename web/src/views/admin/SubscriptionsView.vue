<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Edit, Delete } from '@element-plus/icons-vue'
import { getUISettings, putUISettings, type SubClientRule } from '@/api/settings'

const loading = ref(true)
const saving = ref(false)

// Subscription path
const subPath = ref('sub')

// Client rules
const rules = ref<SubClientRule[]>([])

// Dialog state
const dialogVisible = ref(false)
const dialogMode = ref<'add' | 'edit'>('add')
const editIndex = ref(-1)
const form = reactive<SubClientRule>({
  name: '',
  keywords: [],
  render_format: 'mihomo',
  enabled: true,
})
const keywordsInput = ref('')

async function load() {
  loading.value = true
  try {
    const s = await getUISettings()
    subPath.value = s.sub_path || 'sub'
    rules.value = s.sub_client_rules || []
  } finally {
    loading.value = false
  }
}

async function savePath() {
  saving.value = true
  try {
    const s = await getUISettings()
    s.sub_path = subPath.value || 'sub'
    await putUISettings(s)
    ElMessage.success('订阅路径已保存')
  } finally {
    saving.value = false
  }
}

async function saveRules() {
  saving.value = true
  try {
    const s = await getUISettings()
    s.sub_client_rules = rules.value
    await putUISettings(s)
    ElMessage.success('客户端规则已保存')
  } finally {
    saving.value = false
  }
}

function openAdd() {
  dialogMode.value = 'add'
  editIndex.value = -1
  form.name = ''
  form.keywords = []
  form.render_format = 'mihomo'
  form.enabled = true
  keywordsInput.value = ''
  dialogVisible.value = true
}

function openEdit(index: number) {
  dialogMode.value = 'edit'
  editIndex.value = index
  const rule = rules.value[index]
  form.name = rule.name
  form.keywords = [...rule.keywords]
  form.render_format = rule.render_format
  form.enabled = rule.enabled
  keywordsInput.value = rule.keywords.join(', ')
  dialogVisible.value = true
}

function handleConfirm() {
  if (!form.name.trim()) {
    ElMessage.warning('请输入客户端名称')
    return
  }
  const keywords = keywordsInput.value
    .split(',')
    .map(k => k.trim())
    .filter(k => k)
  if (keywords.length === 0) {
    ElMessage.warning('请输入至少一个关键词')
    return
  }
  const rule: SubClientRule = {
    name: form.name.trim(),
    keywords,
    render_format: form.render_format,
    enabled: form.enabled,
  }
  if (dialogMode.value === 'add') {
    rules.value.push(rule)
  } else {
    rules.value[editIndex.value] = rule
  }
  dialogVisible.value = false
}

function handleDelete(index: number) {
  const rule = rules.value[index]
  ElMessageBox.confirm(
    `确定要删除规则 "${rule.name}" 吗？`,
    '确认删除',
    { confirmButtonText: '删除', cancelButtonText: '取消', type: 'warning' },
  ).then(() => {
    rules.value.splice(index, 1)
  }).catch(() => {})
}

function toggleEnabled(index: number) {
  rules.value[index].enabled = !rules.value[index].enabled
}

onMounted(load)
</script>

<template>
  <div v-loading="loading">
    <!-- Subscription Path -->
    <el-card class="section-card" shadow="never">
      <template #header>
        <span class="card-title">订阅路径</span>
      </template>
      <el-form label-width="100px">
        <el-form-item label="路径前缀">
          <el-input v-model="subPath" placeholder="sub" style="max-width: 300px">
            <template #prepend>/</template>
          </el-input>
          <span class="form-hint" style="margin-left: 12px;">
            完整示例: /{{ subPath }}/&lt;token&gt;
          </span>
        </el-form-item>
        <el-form-item>
          <el-button type="primary" :loading="saving" @click="savePath">保存路径</el-button>
        </el-form-item>
      </el-form>
    </el-card>

    <!-- Client Rules -->
    <el-card class="section-card" shadow="never">
      <template #header>
        <div class="card-header">
          <span class="card-title">客户端规则</span>
          <el-button type="primary" :icon="Plus" @click="openAdd">添加规则</el-button>
        </div>
      </template>
      <p class="section-desc">
        UA 检测优先于 query 参数，禁用的客户端将直接返回 403。规则按顺序匹配，命中第一条即停止。
      </p>
      <el-table :data="rules" stripe>
        <el-table-column prop="name" label="名称" min-width="120" />
        <el-table-column label="关键词" min-width="200">
          <template #default="{ row }">
            <el-tag v-for="kw in row.keywords" :key="kw" size="small" class="keyword-tag">
              {{ kw }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="渲染格式" width="120">
          <template #default="{ row }">
            <el-tag :type="row.render_format === 'sing-box' ? 'success' : 'primary'" size="small">
              {{ row.render_format }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="允许" width="80" align="center">
          <template #default="{ row, $index }">
            <el-switch :model-value="row.enabled" @change="toggleEnabled($index)" />
          </template>
        </el-table-column>
        <el-table-column label="操作" width="120" align="center">
          <template #default="{ $index }">
            <el-button text type="primary" :icon="Edit" @click="openEdit($index)" />
            <el-button text type="danger" :icon="Delete" @click="handleDelete($index)" />
          </template>
        </el-table-column>
      </el-table>
      <div class="table-footer">
        <el-button type="primary" :loading="saving" @click="saveRules">保存规则</el-button>
      </div>
    </el-card>

    <!-- Add/Edit Dialog -->
    <el-dialog
      v-model="dialogVisible"
      :title="dialogMode === 'add' ? '添加规则' : '编辑规则'"
      width="480px"
    >
      <el-form label-width="80px">
        <el-form-item label="名称">
          <el-input v-model="form.name" placeholder="例如: Shadowrocket" />
        </el-form-item>
        <el-form-item label="关键词">
          <el-input v-model="keywordsInput" placeholder="多个用逗号分隔，例如: shadowrocket" />
          <div class="form-hint">从 User-Agent 中匹配的关键词，多个用逗号分隔</div>
        </el-form-item>
        <el-form-item label="渲染格式">
          <el-radio-group v-model="form.render_format">
            <el-radio value="mihomo">mihomo</el-radio>
            <el-radio value="sing-box">sing-box</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="状态">
          <el-checkbox v-model="form.enabled">允许访问</el-checkbox>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" @click="handleConfirm">确定</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.section-card {
  margin-bottom: 20px;
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.card-title {
  font-weight: 600;
  font-size: 16px;
}

.section-desc {
  color: var(--text-muted);
  font-size: 13px;
  margin-bottom: 16px;
}

.keyword-tag {
  margin-right: 4px;
  margin-bottom: 4px;
}

.table-footer {
  margin-top: 16px;
  display: flex;
  justify-content: flex-end;
}

.form-hint {
  color: var(--text-muted);
  font-size: 12px;
  margin-top: 4px;
}
</style>
