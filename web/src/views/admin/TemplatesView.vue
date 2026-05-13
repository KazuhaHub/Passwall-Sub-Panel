<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Delete } from '@element-plus/icons-vue'
import { deleteTemplate, listTemplates, saveTemplate, type Template } from '@/api/templates'

const items = ref<Template[]>([])
const loading = ref(false)
const selectedItems = ref<Template[]>([])
const batchBusy = ref(false)
const selectedCount = computed(() => selectedItems.value.length)
const dialog = ref(false)
const editing = ref(false)
const form = reactive<Template>({
  slug: '',
  name: '',
  client_type: 'clash-meta',
  is_default: false,
  content: '',
})

async function load() {
  loading.value = true
  try {
    items.value = await listTemplates()
    selectedItems.value = []
  } finally {
    loading.value = false
  }
}

function handleSelectionChange(rows: Template[]) {
  selectedItems.value = rows
}

function canSelectTemplate(row: Template) {
  return !row.is_default
}

function openCreate() {
  editing.value = false
  form.slug = ''
  form.name = ''
  form.client_type = 'clash-meta'
  form.is_default = false
  form.content = ''
  dialog.value = true
}

function openEdit(t: Template) {
  editing.value = true
  form.slug = t.slug
  form.name = t.name
  form.client_type = t.client_type
  form.is_default = t.is_default
  form.content = t.content
  dialog.value = true
}

async function submit() {
  if (!form.slug || !form.name) {
    ElMessage.warning('slug 和 name 必填')
    return
  }
  await saveTemplate({ ...form })
  ElMessage.success('已保存')
  dialog.value = false
  await load()
}

async function confirmDelete(t: Template) {
  await ElMessageBox.confirm(`删除模板 ${t.slug}？`, '确认', { type: 'warning' })
  await deleteTemplate(t.slug)
  ElMessage.success('已删除')
  await load()
}

async function batchDelete() {
  if (selectedItems.value.length === 0) return
  const rows = selectedItems.value.slice()
  const names = rows.slice(0, 5).map((row) => row.slug).join('、')
  const suffix = rows.length > 5 ? ` 等 ${rows.length} 个模板` : ''
  try {
    await ElMessageBox.confirm(`确定删除 ${names}${suffix}？`, '批量删除模板', { type: 'warning' })
  } catch {
    return
  }
  batchBusy.value = true
  try {
    const results = await Promise.allSettled(rows.map((row) => deleteTemplate(row.slug)))
    const deletedRows = rows.filter((_, index) => results[index].status === 'fulfilled')
    const failed = rows.length - deletedRows.length
    items.value = items.value.filter((item) => !deletedRows.some((row) => row.slug === item.slug))
    selectedItems.value = []
    if (failed > 0) {
      ElMessage.warning(`已删除 ${deletedRows.length} 个模板，失败 ${failed} 个`)
    } else {
      ElMessage.success(`已删除 ${deletedRows.length} 个模板`)
    }
  } finally {
    batchBusy.value = false
  }
}

onMounted(load)
</script>

<template>
  <div class="psp-page">
    <div class="psp-page-header">
      <div class="psp-page-title">模板</div>
      <el-button type="primary" @click="openCreate">新增模板</el-button>
    </div>

    <div v-if="selectedCount > 0" class="psp-toolbar">
      <span class="selection-count">已选 {{ selectedCount }}</span>
      <el-button
        type="danger"
        :icon="Delete"
        :loading="batchBusy"
        @click="batchDelete"
      >
        批量删除
      </el-button>
    </div>

    <el-table v-loading="loading" :data="items" stripe @selection-change="handleSelectionChange">
      <el-table-column type="selection" width="48" :selectable="canSelectTemplate" />
      <el-table-column prop="slug" label="Slug" min-width="160" />
      <el-table-column prop="name" label="名称" min-width="180" />
      <el-table-column prop="client_type" label="客户端" width="140" />
      <el-table-column label="默认" width="80">
        <template #default="{ row }">
          <el-tag v-if="row.is_default" type="success" size="small">默认</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="200">
        <template #default="{ row }">
          <el-button size="small" @click="openEdit(row)">编辑</el-button>
          <el-button size="small" type="danger" @click="confirmDelete(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog
      v-model="dialog"
      :title="editing ? '编辑模板' : '新增模板'"
      width="900px"
      top="4vh"
    >
      <el-form label-width="100px">
        <el-form-item label="Slug" required>
          <el-input v-model="form.slug" :disabled="editing" />
        </el-form-item>
        <el-form-item label="名称" required>
          <el-input v-model="form.name" />
        </el-form-item>
        <el-form-item label="客户端类型">
          <el-select v-model="form.client_type" style="width: 200px">
            <el-option label="Clash" value="clash" />
            <el-option label="Clash Meta" value="clash-meta" />
            <el-option label="Sing-box" value="sing-box" />
          </el-select>
        </el-form-item>
        <el-form-item label="设为默认">
          <el-switch v-model="form.is_default" />
        </el-form-item>
        <el-form-item label="模板内容">
          <el-input
            v-model="form.content"
            type="textarea"
            :rows="22"
            placeholder="支持 {{ proxies }} / {{ rules_common }} / @all / @region:TW 等占位符"
            class="psp-yaml-editor"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialog = false">取消</el-button>
        <el-button type="primary" @click="submit">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.psp-yaml-editor :deep(textarea) {
  font-family: ui-monospace, 'SFMono-Regular', Menlo, Consolas, monospace;
  font-size: 13px;
  line-height: 1.5;
}

.selection-count {
  color: var(--text-muted);
  white-space: nowrap;
}
</style>
