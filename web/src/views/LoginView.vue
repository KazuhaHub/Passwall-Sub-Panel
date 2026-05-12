<script setup lang="ts">
import { reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { useAuthStore } from '@/stores/auth'
import { ssoLoginURL } from '@/api/auth'

const router = useRouter()
const auth = useAuthStore()

const form = reactive({ username: '', password: '' })
const loading = ref(false)

async function submit() {
  if (!form.username || !form.password) {
    ElMessage.warning('请输入用户名和密码')
    return
  }
  loading.value = true
  try {
    await auth.login(form.username, form.password)
    const returnTo = (router.currentRoute.value.query.return_to as string) || '/admin'
    router.push(returnTo)
  } catch {
    // error toast handled by axios interceptor
  } finally {
    loading.value = false
  }
}

function ssoLogin() {
  const returnTo = (router.currentRoute.value.query.return_to as string) || '/admin'
  location.href = ssoLoginURL(returnTo)
}
</script>

<template>
  <div class="login-page">
    <el-card class="login-card">
      <div class="login-title">Passwall-Sub-Panel</div>
      <el-form @submit.prevent="submit">
        <el-form-item>
          <el-input v-model="form.username" placeholder="用户名" autocomplete="username" />
        </el-form-item>
        <el-form-item>
          <el-input
            v-model="form.password"
            type="password"
            show-password
            placeholder="密码"
            autocomplete="current-password"
          />
        </el-form-item>
        <el-form-item>
          <el-button type="primary" :loading="loading" style="width: 100%" @click="submit">
            登录
          </el-button>
        </el-form-item>
        <el-form-item>
          <el-button plain style="width: 100%" @click="ssoLogin"> 使用 SSO 登录 </el-button>
        </el-form-item>
      </el-form>
    </el-card>
  </div>
</template>

<style scoped>
.login-page {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100vh;
}

.login-card {
  width: 360px;
  padding: 12px;
}

.login-title {
  font-size: 18px;
  font-weight: 600;
  text-align: center;
  margin-bottom: 20px;
}
</style>
