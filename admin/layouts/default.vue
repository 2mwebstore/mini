<script setup lang="ts">
import { useAuthStore } from '~/stores/auth'
import type { Permission } from '~/types/api'
import { roleLabels } from '~/utils/format'

const auth = useAuthStore()
// The router's current route updates as soon as navigation starts (useRoute() in a
// layout waits for the next page to load), so the highlight moves immediately.
const current = useRouter().currentRoute
const menuOpen = ref(false)
watch(() => current.value.fullPath, () => (menuOpen.value = false))

const branchIcon = 'M3 21h18M5 21V8l7-5 7 5v13M9 21v-6h6v6'

// One place for navigation; items are shown only with the permission the API
// will also require.
const items: { to: string; label: string; icon: string; perm?: Permission[] }[] = [
  { to: '/', label: 'Dashboard', icon: 'M3 12 12 3l9 9M5 10v10h5v-6h4v6h5V10' },
  { to: '/reviews', label: 'Reviews', icon: 'M9 12l2 2 4-4M7 4h10a2 2 0 0 1 2 2v14l-7-3-7 3V6a2 2 0 0 1 2-2z', perm: ['team.review', 'all.review'] },
  { to: '/attendance', label: 'Attendance', icon: 'M12 7v5l3 3M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18z' },
  { to: '/leave', label: 'Leave', icon: 'M8 3v3m8-3v3M4 9h16M5 5h14a1 1 0 0 1 1 1v13a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1z' },
  { to: '/employees', label: 'Employees', icon: 'M17 20v-1a4 4 0 0 0-4-4H7a4 4 0 0 0-4 4v1M10 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8zm11 9v-1a4 4 0 0 0-3-3.9M16 3.1a4 4 0 0 1 0 7.8' },
  { to: '/branches', label: 'Branches', icon: branchIcon, perm: ['branches.manage'] },
  { to: '/reports', label: 'Reports', icon: 'M4 20V10m6 10V4m6 16v-7m4 7H2' },
  { to: '/settings', label: 'Settings', icon: 'M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6zm7.4-3a7.4 7.4 0 0 0-.1-1.2l2-1.6-2-3.4-2.4 1a7.5 7.5 0 0 0-2-1.2L14.5 3h-5l-.4 2.6a7.5 7.5 0 0 0-2 1.2l-2.4-1-2 3.4 2 1.6a7.4 7.4 0 0 0 0 2.4l-2 1.6 2 3.4 2.4-1a7.5 7.5 0 0 0 2 1.2l.4 2.6h5l.4-2.6a7.5 7.5 0 0 0 2-1.2l2.4 1 2-3.4-2-1.6c.1-.4.1-.8.1-1.2z', perm: ['settings.view'] },
  { to: '/audit', label: 'Audit log', icon: 'M9 5h10M9 12h10M9 19h10M5 5h.01M5 12h.01M5 19h.01', perm: ['audit.view'] },
]
const nav = computed(() => {
  const list = items.filter(i => !i.perm || i.perm.some(p => auth.can(p)))
  // A branch admin edits their own branch's settings from "My branch".
  const myBranch = auth.employee?.branch_id
  if (!auth.can('branches.manage') && auth.can('branch.settings') && myBranch) {
    list.splice(list.findIndex(i => i.to === '/reports') + 1, 0, { to: `/branches/${myBranch}`, label: 'My branch', icon: branchIcon })
  }
  return list
})
const active = (to: string) => (to === '/' ? current.value.path === '/' : current.value.path.startsWith(to))

async function signOut() {
  await auth.logout()
  await navigateTo('/login')
}
</script>

<template>
  <div class="min-h-screen lg:grid lg:grid-cols-[15rem_1fr]">
    <!-- mobile top bar -->
    <div class="sticky top-0 z-30 flex items-center justify-between border-b border-border bg-surface px-4 py-3 lg:hidden">
      <span class="font-semibold">{{ auth.company.name || 'Attendance' }}</span>
      <button type="button" class="rounded-md p-1.5 hover:bg-surface-2" :aria-expanded="menuOpen" aria-controls="sidebar" aria-label="Menu" @click="menuOpen = !menuOpen">
        <svg viewBox="0 0 24 24" class="size-5" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="M4 6h16M4 12h16M4 18h16" /></svg>
      </button>
    </div>
    <div v-if="menuOpen" class="fixed inset-0 z-30 bg-black/30 lg:hidden" @click="menuOpen = false" />

    <aside
      id="sidebar"
      class="fixed inset-y-0 left-0 z-40 flex w-60 flex-col border-r border-border bg-surface transition-transform lg:sticky lg:top-0 lg:h-screen lg:translate-x-0"
      :class="menuOpen ? 'translate-x-0' : '-translate-x-full'"
    >
      <div class="px-5 py-5">
        <p class="text-xs font-medium uppercase tracking-wide text-muted">Attendance admin</p>
        <p class="mt-0.5 truncate font-semibold">{{ auth.company.name }}</p>
        <p v-if="auth.employee?.branch && auth.role === 'branch_admin'" class="truncate text-sm text-muted">{{ auth.employee.branch.name }}</p>
      </div>
      <nav class="flex-1 overflow-y-auto px-3" aria-label="Main">
        <ul class="flex flex-col gap-0.5">
          <li v-for="i in nav" :key="i.to">
            <NuxtLink
              :to="i.to"
              class="flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium transition"
              :class="active(i.to) ? 'bg-primary/10 text-primary' : 'text-muted hover:bg-surface-2 hover:text-fg'"
              :aria-current="active(i.to) ? 'page' : undefined"
            >
              <svg viewBox="0 0 24 24" class="size-[18px] shrink-0" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path :d="i.icon" /></svg>
              {{ i.label }}
            </NuxtLink>
          </li>
        </ul>
      </nav>
      <div class="border-t border-border px-5 py-4">
        <p class="truncate text-sm font-medium">{{ auth.employee?.full_name }}</p>
        <p class="text-xs text-muted">{{ auth.role ? roleLabels[auth.role] : '' }}</p>
        <AppButton class="mt-3 w-full" size="sm" variant="secondary" @click="signOut">Sign out</AppButton>
      </div>
    </aside>

    <main class="min-w-0 px-4 py-6 sm:px-8">
      <div class="mx-auto max-w-6xl"><slot /></div>
    </main>
  </div>
</template>
