<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Delete } from '@element-plus/icons-vue'
import { deleteTemplate, listTemplates, saveTemplate, type Template } from '@/api/templates'
import { listRuleSets, type RuleSet } from '@/api/rules'

const items = ref<Template[]>([])
const ruleSets = ref<RuleSet[]>([])
const loading = ref(false)
const selectedItems = ref<Template[]>([])
const batchBusy = ref(false)
const selectedCount = computed(() => selectedItems.value.length)
const dialog = ref(false)
const editing = ref(false)
const form = reactive<Template>({
  slug: '',
  name: '',
  client_type: 'mihomo',
  is_default: false,
  rule_sets: [],
  content: '',
})

const builtInTargets = new Set(['DIRECT', 'REJECT', 'REJECT-DROP', 'REJECT-DROP-BIT', 'PASS'])

const selectedRuleSetDetails = computed(() =>
  form.rule_sets
    .map((slug) => ruleSets.value.find((item) => item.slug === slug))
    .filter((item): item is RuleSet => Boolean(item)),
)

const formProxyGroups = computed(() => extractProxyGroups(form.content))
const formRuleTargets = computed(() => extractRuleTargets(selectedRuleSetDetails.value))
const formMissingTargets = computed(() => missingTargets(formProxyGroups.value, formRuleTargets.value))
const formDynamicProxyGroups = computed(() => usesDynamicProxyGroups(form.content))

async function load() {
  loading.value = true
  try {
    const [templateItems, ruleSetItems] = await Promise.all([listTemplates(), listRuleSets()])
    items.value = templateItems
    ruleSets.value = ruleSetItems
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
  form.client_type = 'mihomo'
  form.is_default = false
  form.rule_sets = []
  form.content = ''
  dialog.value = true
}

function openEdit(t: Template) {
  editing.value = true
  form.slug = t.slug
  form.name = t.name
  form.client_type = t.client_type
  form.is_default = t.is_default
  form.rule_sets = t.rule_sets ? t.rule_sets.slice() : []
  form.content = t.content
  dialog.value = true
}

function ruleSetName(slug: string) {
  return ruleSets.value.find((item) => item.slug === slug)?.name || slug
}

function ruleSetSummary(row: Template) {
  if (!row.rule_sets || row.rule_sets.length === 0) return '未绑定'
  return row.rule_sets.map(ruleSetName).join('、')
}

function templateRuleSets(row: Template) {
  return (row.rule_sets || [])
    .map((slug) => ruleSets.value.find((item) => item.slug === slug))
    .filter((item): item is RuleSet => Boolean(item))
}

function normalizeTarget(raw: string) {
  return raw.trim().replace(/^['"]|['"]$/g, '')
}

function extractProxyGroups(content: string) {
  const groups = new Set<string>()
  let inProxyGroups = false
  for (const line of content.split('\n')) {
    if (/^proxy-groups\s*:/.test(line)) {
      inProxyGroups = true
      continue
    }
    if (inProxyGroups && /^\S/.test(line) && !/^proxy-groups\s*:/.test(line)) {
      break
    }
    const match = line.match(/^\s*-\s*name:\s*(.+?)\s*$/)
    if (inProxyGroups && match) {
      groups.add(normalizeTarget(match[1]))
    }
  }
  return groups
}

function usesDynamicProxyGroups(content: string) {
  // Check for both mihomo ({{ proxy_groups }}) and sing-box ({{ outbounds }}) placeholders
  return /\{\{\s*proxy_groups\s*\}\}/.test(content) || /\{\{\s*outbounds\s*\}\}/.test(content)
}

function extractRuleTargets(sets: RuleSet[]) {
  const targets = new Set<string>()
  for (const rs of sets) {
    if (!rs.enabled) continue
    for (const rawLine of rs.content.split('\n')) {
      const line = rawLine.trim().replace(/^-\s*/, '')
      if (!line || line.startsWith('#') || line.includes('{{')) continue
      const parts = line.split(',').map((part) => normalizeTarget(part))
      if (parts.length < 2) continue
      const usefulParts = parts.filter((part) => part && part !== 'no-resolve')
      const target = usefulParts[usefulParts.length - 1]
      if (target && !builtInTargets.has(target)) {
        targets.add(target)
      }
    }
  }
  return targets
}

function missingTargets(groups: Set<string>, targets: Set<string>) {
  if (groups.size === 0) return Array.from(targets)
  return Array.from(targets).filter((target) => !groups.has(target))
}

function coverageState(row: Template) {
  const sets = templateRuleSets(row)
  if (sets.length === 0) return { type: 'info', text: '未绑定' }
  if (usesDynamicProxyGroups(row.content)) return { type: 'success', text: '自动生成' }
  const groups = extractProxyGroups(row.content)
  const missing = missingTargets(groups, extractRuleTargets(sets))
  if (missing.length > 0) return { type: 'warning', text: `缺失 ${missing.length}` }
  return { type: 'success', text: '正常' }
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
  await ElMessageBox.confirm(`删除配置方案 ${t.slug}？`, '确认', { type: 'warning' })
  await deleteTemplate(t.slug)
  ElMessage.success('已删除')
  await load()
}

async function batchDelete() {
  if (selectedItems.value.length === 0) return
  const rows = selectedItems.value.slice()
  const names = rows.slice(0, 5).map((row) => row.slug).join('、')
  const suffix = rows.length > 5 ? ` 等 ${rows.length} 个配置方案` : ''
  try {
    await ElMessageBox.confirm(`确定删除 ${names}${suffix}？`, '批量删除配置方案', { type: 'warning' })
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
      ElMessage.warning(`已删除 ${deletedRows.length} 个配置方案，失败 ${failed} 个`)
    } else {
      ElMessage.success(`已删除 ${deletedRows.length} 个配置方案`)
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
      <div>
        <div class="psp-page-title">配置方案</div>
        <div class="psp-page-desc">一个方案由客户端模板和绑定规则集组成，订阅渲染会以这里的绑定为准。</div>
      </div>
      <el-button type="primary" @click="openCreate">新增方案</el-button>
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
      <el-table-column label="规则集" min-width="220" show-overflow-tooltip>
        <template #default="{ row }">{{ ruleSetSummary(row) }}</template>
      </el-table-column>
      <el-table-column label="规则目标" width="110">
        <template #default="{ row }">
          <el-tag :type="coverageState(row).type" size="small">{{ coverageState(row).text }}</el-tag>
        </template>
      </el-table-column>
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
      :title="editing ? '编辑配置方案' : '新增配置方案'"
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
            <el-option label="mihomo" value="mihomo" />
            <el-option label="Sing-box" value="sing-box" />
          </el-select>
        </el-form-item>
        <el-form-item label="设为默认">
          <el-switch v-model="form.is_default" />
        </el-form-item>
        <el-form-item label="规则集">
          <el-select
            v-model="form.rule_sets"
            multiple
            clearable
            filterable
            collapse-tags
            collapse-tags-tooltip
            placeholder="选择与该模板策略组匹配的规则集"
            style="width: 100%"
          >
            <el-option
              v-for="ruleSet in ruleSets"
              :key="ruleSet.slug"
              :label="`${ruleSet.name} (${ruleSet.slug})`"
              :value="ruleSet.slug"
              :disabled="!ruleSet.enabled"
            />
          </el-select>
          <div class="form-hint">订阅渲染只使用这里绑定的规则集；未绑定时 <code v-pre>{{ rules_common }}</code> 为空。模板可用 <code v-pre>{{ proxy_groups }}</code> 让策略组跟随规则目标自动生成。</div>
          <div v-if="form.rule_sets.length > 0" class="binding-summary">
            <el-tag
              v-for="ruleSet in selectedRuleSetDetails"
              :key="ruleSet.slug"
              :type="ruleSet.enabled ? 'success' : 'info'"
              size="small"
            >
              {{ ruleSet.name }}
            </el-tag>
          </div>
          <div v-if="formDynamicProxyGroups && form.rule_sets.length > 0" class="target-ok">
            策略组将根据已绑定规则集的规则目标自动生成。
          </div>
          <div v-else-if="formMissingTargets.length > 0 && form.content.length > 50" class="target-warning">
            规则目标未在模板策略组中找到：{{ formMissingTargets.slice(0, 8).join('、') }}{{ formMissingTargets.length > 8 ? ` 等 ${formMissingTargets.length} 个` : '' }}
          </div>
          <div v-else-if="form.rule_sets.length > 0 && formProxyGroups.size > 0" class="target-ok">
            已识别 {{ formProxyGroups.size }} 个策略组，规则目标匹配正常。
          </div>
          <div v-else-if="form.rule_sets.length === 0" class="target-empty">
            当前未绑定规则集，只会渲染节点、策略组和个人规则。
          </div>
          <div v-else-if="form.content.length < 50" class="target-empty">
            模板内容尚未完整，请继续编辑。
          </div>
        </el-form-item>
        <el-form-item label="模板内容">
          <el-input
            v-model="form.content"
            type="textarea"
            :rows="22"
            placeholder="支持 {{ proxies }} / {{ proxy_groups }} / {{ rules_common }} / {{ rules_personal }} / @all / @region:TW 等占位符"
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

.psp-page-desc {
  margin-top: 6px;
  color: var(--text-muted);
  font-size: 13px;
}

.form-hint {
  margin-top: 6px;
  color: var(--text-muted);
  font-size: 12px;
  line-height: 1.4;
}

.binding-summary {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 8px;
}

.target-warning,
.target-ok,
.target-empty {
  margin-top: 8px;
  font-size: 12px;
  line-height: 1.5;
}

.target-warning {
  color: var(--el-color-warning);
}

.target-ok {
  color: var(--el-color-success);
}

.target-empty {
  color: var(--text-muted);
}
</style>
