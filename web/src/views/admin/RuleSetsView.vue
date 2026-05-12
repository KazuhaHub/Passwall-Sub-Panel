<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  deleteRuleSet,
  listRuleSets,
  saveRuleSet,
  type RuleSet,
} from '@/api/rules'

const items = ref<RuleSet[]>([])
const loading = ref(false)
const dialog = ref(false)
const editing = ref(false)
const form = reactive<RuleSet>({
  slug: '',
  name: '',
  sort: 100,
  enabled: true,
  content: '',
})

async function load() {
  loading.value = true
  try {
    items.value = await listRuleSets()
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editing.value = false
  form.slug = ''
  form.name = ''
  form.sort = 100
  form.enabled = true
  form.content = ''
  dialog.value = true
}

function openEdit(rs: RuleSet) {
  editing.value = true
  form.slug = rs.slug
  form.name = rs.name
  form.sort = rs.sort
  form.enabled = rs.enabled
  form.content = rs.content
  dialog.value = true
}

async function submit() {
  if (!form.slug || !form.name) {
    ElMessage.warning('slug 和 name 必填')
    return
  }
  await saveRuleSet({ ...form })
  ElMessage.success('已保存')
  dialog.value = false
  await load()
}

async function confirmDelete(rs: RuleSet) {
  await ElMessageBox.confirm(`删除规则集 ${rs.slug}？`, '确认', { type: 'warning' })
  await deleteRuleSet(rs.slug)
  ElMessage.success('已删除')
  await load()
}

function countLines(s: string): number {
  return s ? s.split('\n').filter((l) => l.trim()).length : 0
}

onMounted(load)
</script>

<template>
  <div class="psp-page">
    <div class="psp-page-header">
      <div class="psp-page-title">规则集</div>
      <el-button type="primary" @click="openCreate">新增规则集</el-button>
    </div>

    <el-table v-loading="loading" :data="items" stripe>
      <el-table-column prop="slug" label="Slug" min-width="140" />
      <el-table-column prop="name" label="名称" min-width="160" />
      <el-table-column prop="sort" label="排序" width="80" />
      <el-table-column label="规则行数" width="100">
        <template #default="{ row }">{{ countLines(row.content) }}</template>
      </el-table-column>
      <el-table-column label="状态" width="100">
        <template #default="{ row }">
          <el-tag :type="row.enabled ? 'success' : 'info'" size="small">
            {{ row.enabled ? '启用' : '禁用' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="200" fixed="right">
        <template #default="{ row }">
          <el-button size="small" @click="openEdit(row)">编辑</el-button>
          <el-button size="small" type="danger" @click="confirmDelete(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog
      v-model="dialog"
      :title="editing ? '编辑规则集' : '新增规则集'"
      width="720px"
      top="6vh"
    >
      <el-form label-width="100px">
        <el-form-item label="Slug" required>
          <el-input v-model="form.slug" :disabled="editing" placeholder="ad_block" />
        </el-form-item>
        <el-form-item label="名称" required>
          <el-input v-model="form.name" placeholder="广告拦截" />
        </el-form-item>
        <el-form-item label="排序">
          <el-input-number v-model="form.sort" />
        </el-form-item>
        <el-form-item label="启用">
          <el-switch v-model="form.enabled" />
        </el-form-item>
        <el-form-item label="规则内容">
          <el-input
            v-model="form.content"
            type="textarea"
            :rows="16"
            placeholder="- DOMAIN-SUFFIX,example.com,DIRECT"
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
</style>
