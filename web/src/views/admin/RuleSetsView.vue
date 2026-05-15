<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import LineIcon from '@/components/LineIcon.vue'
import {
  deleteRuleSet,
  listRuleSets,
  saveRuleSet,
  type RuleSet,
} from '@/api/rules'
import { listTemplates, type Template } from '@/api/templates'

const items = ref<RuleSet[]>([])
const templates = ref<Template[]>([])
const loading = ref(false)
const selectedItems = ref<RuleSet[]>([])
const batchBusy = ref<'enable' | 'disable' | 'delete' | ''>('')
const selectedCount = computed(() => selectedItems.value.length)
const dialog = ref(false)
const editing = ref(false)
const proxyGroupOrderText = ref('')
const form = reactive<RuleSet>({
  slug: '',
  name: '',
  sort: 100,
  enabled: true,
  proxy_group_order: [],
  content: '',
})

async function load() {
  loading.value = true
  try {
    const [ruleSetItems, templateItems] = await Promise.all([listRuleSets(), listTemplates()])
    items.value = ruleSetItems
    templates.value = templateItems
    selectedItems.value = []
  } finally {
    loading.value = false
  }
}

function handleSelectionChange(rows: RuleSet[]) {
  selectedItems.value = rows
}

function openCreate() {
  editing.value = false
  form.slug = ''
  form.name = ''
  form.sort = 100
  form.enabled = true
  form.proxy_group_order = []
  proxyGroupOrderText.value = ''
  form.content = ''
  dialog.value = true
}

function openEdit(rs: RuleSet) {
  editing.value = true
  form.slug = rs.slug
  form.name = rs.name
  form.sort = rs.sort
  form.enabled = rs.enabled
  form.proxy_group_order = rs.proxy_group_order ? rs.proxy_group_order.slice() : []
  proxyGroupOrderText.value = form.proxy_group_order.join('\n')
  form.content = rs.content
  dialog.value = true
}

async function submit() {
  if (!form.slug || !form.name) {
    ElMessage.warning('slug 和 name 必填')
    return
  }
  const proxyGroupOrder = proxyGroupOrderText.value
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)
  await saveRuleSet({ ...form, proxy_group_order: proxyGroupOrder })
  ElMessage.success('已保存')
  dialog.value = false
  await load()
}

async function confirmDelete(rs: RuleSet) {
  const usedBy = usedByTemplates(rs)
  const usageText = usedBy.length > 0
    ? `\n\n该规则集正在被 ${usedBy.map((tpl) => tpl.name || tpl.slug).join('、')} 引用，删除后这些配置方案会跳过该规则集。`
    : ''
  await ElMessageBox.confirm(`删除规则集 ${rs.slug}？${usageText}`, '确认', { type: 'warning' })
  await deleteRuleSet(rs.slug)
  ElMessage.success('已删除')
  await load()
}

async function batchSetEnabled(enabled: boolean) {
  if (selectedItems.value.length === 0) return
  const rows = selectedItems.value.slice()
  batchBusy.value = enabled ? 'enable' : 'disable'
  try {
    const results = await Promise.allSettled(rows.map((row) => saveRuleSet({ ...row, enabled })))
    const failed = results.filter((result) => result.status === 'rejected').length
    if (failed > 0) {
      ElMessage.warning(`已${enabled ? '启用' : '禁用'} ${rows.length - failed} 个规则集，失败 ${failed} 个`)
    } else {
      ElMessage.success(`已${enabled ? '启用' : '禁用'} ${rows.length} 个规则集`)
    }
    await load()
  } finally {
    batchBusy.value = ''
  }
}

async function batchDelete() {
  if (selectedItems.value.length === 0) return
  const rows = selectedItems.value.slice()
  const names = rows.slice(0, 5).map((row) => row.slug).join('、')
  const suffix = rows.length > 5 ? ` 等 ${rows.length} 个规则集` : ''
  try {
    await ElMessageBox.confirm(`确定删除 ${names}${suffix}？`, '批量删除规则集', { type: 'warning' })
  } catch {
    return
  }
  batchBusy.value = 'delete'
  try {
    const results = await Promise.allSettled(rows.map((row) => deleteRuleSet(row.slug)))
    const deletedRows = rows.filter((_, index) => results[index].status === 'fulfilled')
    const failed = rows.length - deletedRows.length
    items.value = items.value.filter((item) => !deletedRows.some((row) => row.slug === item.slug))
    selectedItems.value = []
    if (failed > 0) {
      ElMessage.warning(`已删除 ${deletedRows.length} 个规则集，失败 ${failed} 个`)
    } else {
      ElMessage.success(`已删除 ${deletedRows.length} 个规则集`)
    }
  } finally {
    batchBusy.value = ''
  }
}

function countLines(s: string): number {
  return s ? s.split('\n').filter((l) => l.trim()).length : 0
}

function usedByTemplates(rs: RuleSet) {
  return templates.value.filter((tpl) => (tpl.rule_sets || []).includes(rs.slug))
}

function usageSummary(rs: RuleSet) {
  const usedBy = usedByTemplates(rs)
  if (usedBy.length === 0) return '未引用'
  return usedBy.map((tpl) => tpl.name || tpl.slug).join('、')
}

onMounted(load)
</script>

<template>
  <div class="psp-page">
    <div class="psp-page-header">
      <div>
        <div class="psp-page-title">规则库</div>
        <div class="psp-page-desc">规则集是可复用片段，只有绑定到配置方案后才会参与订阅渲染。</div>
      </div>
      <el-button type="primary" @click="openCreate">新增规则集</el-button>
    </div>

    <div v-if="selectedCount > 0" class="psp-toolbar">
      <span class="selection-count">已选 {{ selectedCount }}</span>
      <el-button
        :loading="batchBusy === 'enable'"
        :disabled="batchBusy !== ''"
        @click="batchSetEnabled(true)"
      >
        <LineIcon name="unlock" :size="16" />
        批量启用
      </el-button>
      <el-button
        :loading="batchBusy === 'disable'"
        :disabled="batchBusy !== ''"
        @click="batchSetEnabled(false)"
      >
        <LineIcon name="lock" :size="16" />
        批量禁用
      </el-button>
      <el-button
        type="danger"
        :loading="batchBusy === 'delete'"
        :disabled="batchBusy !== ''"
        @click="batchDelete"
      >
        <LineIcon name="trash" :size="16" />
        批量删除
      </el-button>
    </div>

    <el-table v-loading="loading" :data="items" stripe @selection-change="handleSelectionChange">
      <el-table-column type="selection" width="48" />
      <el-table-column prop="slug" label="Slug" min-width="140" />
      <el-table-column prop="name" label="名称" min-width="160" />
      <el-table-column prop="sort" label="排序" width="80" />
      <el-table-column label="规则行数" width="100">
        <template #default="{ row }">{{ countLines(row.content) }}</template>
      </el-table-column>
      <el-table-column label="引用配置方案" min-width="220" show-overflow-tooltip>
        <template #default="{ row }">
          <span :class="{ muted: usedByTemplates(row).length === 0 }">{{ usageSummary(row) }}</span>
        </template>
      </el-table-column>
      <el-table-column label="状态" width="100">
        <template #default="{ row }">
          <el-tag :type="row.enabled ? 'success' : 'info'" size="small">
            {{ row.enabled ? '启用' : '禁用' }}
          </el-tag>
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
        <el-form-item label="策略组顺序">
          <el-input
            v-model="proxyGroupOrderText"
            type="textarea"
            :rows="6"
            placeholder="🚀 节点选择&#10;💬 Ai平台&#10;🎮 游戏平台"
            class="psp-yaml-editor"
          />
          <div class="form-hint">
            可留空；一行一个策略组名。绑定该规则集的订阅会优先按这里排列，未列出的策略组保持规则内容中的首次出现顺序。
          </div>
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

.selection-count {
  color: var(--text-muted);
  white-space: nowrap;
}

.psp-page-desc,
.muted {
  color: var(--text-muted);
}

.psp-page-desc {
  margin-top: 6px;
  font-size: 13px;
}

.form-hint {
  margin-top: 6px;
  color: var(--text-muted);
  font-size: 12px;
  line-height: 1.4;
}
</style>
