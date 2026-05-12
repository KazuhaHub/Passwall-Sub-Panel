import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'

import LoginView from '@/views/LoginView.vue'
import AdminLayout from '@/layouts/AdminLayout.vue'
import UserLayout from '@/layouts/UserLayout.vue'

const routes: RouteRecordRaw[] = [
  { path: '/', redirect: '/admin/dashboard' },
  { path: '/login', component: LoginView, meta: { public: true } },
  {
    path: '/admin',
    component: AdminLayout,
    redirect: '/admin/dashboard',
    meta: { requiresAdmin: true },
    children: [
      { path: 'dashboard', component: () => import('@/views/admin/DashboardView.vue') },
      { path: 'users', component: () => import('@/views/admin/UsersView.vue') },
      { path: 'nodes', component: () => import('@/views/admin/NodesView.vue') },
      { path: 'groups', component: () => import('@/views/admin/GroupsView.vue') },
      { path: 'rules', component: () => import('@/views/admin/RuleSetsView.vue') },
      { path: 'templates', component: () => import('@/views/admin/TemplatesView.vue') },
      { path: 'traffic', component: () => import('@/views/admin/TrafficView.vue') },
      { path: 'audit', component: () => import('@/views/admin/AuditView.vue') },
    ],
  },
  {
    path: '/user',
    component: UserLayout,
    redirect: '/user/me',
    children: [{ path: 'me', component: () => import('@/views/user/MeView.vue') }],
  },
]

const router = createRouter({
  history: createWebHistory(),
  routes,
})

router.beforeEach((to) => {
  if (to.meta.public) return true
  const token = sessionStorage.getItem('psp_access')
  if (!token) {
    return { path: '/login', query: { return_to: to.fullPath } }
  }
  return true
})

export default router
