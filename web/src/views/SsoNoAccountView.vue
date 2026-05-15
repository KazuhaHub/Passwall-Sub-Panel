<template>
  <div class="no-account">
    <el-result
      title="未找到账户"
      sub-title="您的 SSO 身份未关联任何账户，请联系管理员为您创建账户后再试。"
    >
      <template #icon>
        <LineIcon name="warning" :size="64" />
      </template>
      <template #extra>
        <el-button @click="router.replace('/login')">返回登录</el-button>
      </template>
    </el-result>
  </div>
</template>

<script setup lang="ts">
import { useRouter } from 'vue-router'
import LineIcon from '@/components/LineIcon.vue'

const router = useRouter()
</script>

<style scoped>
.no-account {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100vh;
}
</style>
