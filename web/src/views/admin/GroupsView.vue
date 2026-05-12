<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { createGroup, deleteGroup, listGroups, updateGroup } from '@/api/groups'
import type { Group } from '@/api/types'

const groups = ref<Group[]>([])
const loading = ref(false)

const dialog = ref(false)
const editing = ref<Group | null>(null)
const form = reactive({
  slug: '',
  name: '',
  all: false,
  tags_text: '',
  remark: '',
})

async function load() {
  loading.value = true
  try {
    const res = await listGroups()
    groups.value = res.items
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editing.value = null
  form.slug = ''
  form.name = ''
  form.all = false
  form.tags_text = ''
  form.remark = ''
  dialog.value = true
}

function openEdit(g: Group) {
  editing.value = g
  form.slug = g.slug
  form.name = g.name
  form.all = g.tag_filter.all
  form.tags_text = g.tag_filter.tags.join(', ')
  form.remark = g.remark || ''
  dialog.value = true
}

async function submit() {
  const tagFilter = {
    all: form.all,
    tags: form.all
      ? []
      : form.tags_text
          .split(',')
          .map((t) => t.trim())
          .filter(Boolean),
  }
  if (editing.value) {
    const res = await updateGroup(editing.value.id, {
      name: form.name,
      tag_filter: tagFilter,
      remark: form.remark,
    })
    ElMessage.success('已更新')
    if (res.resync_errors?.length) {
      ElMessage.warning(`部分成员 resync 失败：${res.resync_errors.length} 个`)
    }
  } else {
    await createGroup({
      slug: form.slug,
      name: form.name,
      tag_filter: tagFilter,
      remark: form.remark,
    })
    ElMessage.success('已创建')
  }
  dialog.value = false
  await load()
}

async function confirmDelete(g: Group) {
  if (g.members > 0) {
    ElMessage.warning(`该组还有 ${g.members} 个成员，请先迁出`)
    return
  }
  await ElMessageBox.confirm(`确定删除分组 ${g.name}？`, '确认', { type: 'warning' })
  await deleteGroup(g.id)
  ElMessage.success('已删除')
  await load()
}

onMounted(load)
</script>

<template>
  <div class="psp-page">
    <div class="psp-page-header">
      <div class="psp-page-title">分组管理</div>
      <el-button type="primary" @click="openCreate">新增分组</el-button>
    </div>

    <el-table v-loading="loading" :data="groups" stripe>
      <el-table-column prop="name" label="名称" min-width="160" />
      <el-table-column prop="slug" label="Slug" min-width="120" />
      <el-table-column label="tag_filter" min-width="240">
        <template #default="{ row }">
          <el-tag v-if="row.tag_filter.all" type="success" size="small">所有节点</el-tag>
          <el-tag
            v-for="t in row.tag_filter.tags"
            :key="t"
            size="small"
            style="margin-right: 4px"
          >
            {{ t }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="members" label="成员数" width="100" />
      <el-table-column prop="remark" label="备注" min-width="200" />
      <el-table-column label="操作" width="180" fixed="right">
        <template #default="{ row }">
          <el-button size="small" @click="openEdit(row)">编辑</el-button>
          <el-button size="small" type="danger" @click="confirmDelete(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="dialog" :title="editing ? '编辑分组' : '新增分组'" width="500px">
      <el-form label-width="120px">
        <el-form-item label="Slug" required>
          <el-input v-model="form.slug" :disabled="!!editing" />
        </el-form-item>
        <el-form-item label="名称" required>
          <el-input v-model="form.name" />
        </el-form-item>
        <el-form-item label="匹配所有节点">
          <el-switch v-model="form.all" />
        </el-form-item>
        <el-form-item v-if="!form.all" label="tag_filter">
          <el-input
            v-model="form.tags_text"
            placeholder="region:TW, tag:reality"
            type="textarea"
            :rows="2"
          />
          <div style="color: #909399; font-size: 12px; margin-top: 4px">
            逗号分隔，AND 组合。支持 region:XX / tag:YY / server:ZZ
          </div>
        </el-form-item>
        <el-form-item label="备注">
          <el-input v-model="form.remark" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialog = false">取消</el-button>
        <el-button type="primary" @click="submit">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>
