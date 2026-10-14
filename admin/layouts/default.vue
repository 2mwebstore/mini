<script setup lang="ts">
import { useAuthStore } from '~/stores/auth'
import type { Permission } from '~/types/api'

const auth = useAuthStore()
// The router's current route updates as soon as navigation starts (useRoute() in a
// layout waits for the next page to load), so the highlight moves immediately.
const current = useRouter().currentRoute
const menuOpen = ref(false)
watch(() => current.value.fullPath, () => (menuOpen.value = false))

const branchIcon = 'M3 21h18M5 21V8l7-5 7 5v13M9 21v-6h6v6'

type NavLink = { to: string; label: string; perm?: Permission[] }
type NavItem = { label: string; icon: string; to?: string; perm?: Permission[]; children?: NavLink[] }

// One place for navigation; items are shown only with the permission the API
// will also require. Groups open as sub-menus.
const items: NavItem[] = [
  { to: '/', label: 'Dashboard', icon: 'M3 12 12 3l9 9M5 10v10h5v-6h4v6h5V10' },
  {
    label: 'Attendance', icon: 'M12 7v5l3 3M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18z',
    children: [
      { to: '/attendance', label: 'Records' },
      { to: '/reports', label: 'Reports' },
    ],
  },
  {
    label: 'Leave', icon: 'M12 3a9 9 0 0 1 9 9H3a9 9 0 0 1 9-9zm0 9v9m-3 0h6',
    children: [
      { to: '/leave', label: 'Requests' },
      { to: '/leave/balances', label: 'Balances' },
      { to: '/leave/types', label: 'Types' },
    ],
  },
  {
    label: 'Schedules', icon: 'M8 3v3m8-3v3M4 9h16M5 5h14a1 1 0 0 1 1 1v13a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1zm3 8h2m4 0h2m-8 4h2',
    children: [
      { to: '/schedules/roster', label: 'Weekly roster' },
      { to: '/schedules/special', label: 'Special schedules' },
      { to: '/schedules/shifts', label: 'Shifts' },
      { to: '/schedules/holidays', label: 'Holidays' },
    ],
  },
  {
    label: 'Employees', icon: 'M17 20v-1a4 4 0 0 0-4-4H7a4 4 0 0 0-4 4v1M10 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8zm11 9v-1a4 4 0 0 0-3-3.9M16 3.1a4 4 0 0 1 0 7.8',
    children: [
      { to: '/employees', label: 'Employees' },
      { to: '/employees/departments', label: 'Departments' },
      { to: '/employees/positions', label: 'Positions' },
    ],
  },
  { to: '/branches', label: 'Branches', icon: branchIcon, perm: ['branches.manage'] },
  {
    label: 'Administration', icon: 'M12 3 4 6v6c0 5 3.4 8.6 8 9 4.6-.4 8-4 8-9V6zm-3 9 2 2 4-4', perm: ['users.manage'],
    children: [
      { to: '/access/users', label: 'System users' },
      { to: '/access/branches', label: 'Branch assignment' },
      { to: '/access/roles', label: 'Role management' },
    ],
  },
  {
    label: 'Settings', icon: 'M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6zm7.4-3a7.4 7.4 0 0 0-.1-1.2l2-1.6-2-3.4-2.4 1a7.5 7.5 0 0 0-2-1.2L14.5 3h-5l-.4 2.6a7.5 7.5 0 0 0-2 1.2l-2.4-1-2 3.4 2 1.6a7.4 7.4 0 0 0 0 2.4l-2 1.6 2 3.4 2.4-1a7.5 7.5 0 0 0 2 1.2l.4 2.6h5l.4-2.6a7.5 7.5 0 0 0 2-1.2l2.4 1 2-3.4-2-1.6c.1-.4.1-.8.1-1.2z',
    children: [
      { to: '/settings', label: 'General', perm: ['settings.view'] },
      { to: '/settings/location', label: 'Location (GPS)', perm: ['settings.view', 'branch.settings'] },
      { to: '/audit', label: 'Audit log', perm: ['audit.view'] },
    ],
  },
]
const allowed = (perm?: Permission[]) => !perm || perm.some(p => auth.can(p))
const nav = computed(() => {
  const list = items
    .filter(i => allowed(i.perm))
    .map(i => (i.children ? { ...i, children: i.children.filter(c => allowed(c.perm)) } : i))
    .filter(i => !i.children || i.children.length > 0)
  // A branch admin edits their own branch's settings from "My branch".
  const myBranch = auth.employee?.branch_id
  if (!auth.can('branches.manage') && auth.can('branch.settings') && myBranch) {
    list.splice(list.findIndex(i => i.label === 'Employees') + 1, 0, { to: `/branches/${myBranch}`, label: 'My branch', icon: branchIcon })
  }
  return list
})
// The most specific link containing the current page is the active one
// (/leave/types highlights "Types", not also "Requests").
const activeTo = computed(() => {
  const path = current.value.path
  const tos = nav.value.flatMap(i => (i.children ? i.children.map(c => c.to) : i.to ? [i.to] : []))
  return tos.filter(to => (to === '/' ? path === '/' : path === to || path.startsWith(`${to}/`))).sort((a, b) => b.length - a.length)[0]
})
const active = (to: string) => activeTo.value === to

// A group is open while one of its pages is shown, or after the user opens it.
const toggled = ref<Record<string, boolean>>({})
const groupActive = (i: NavItem) => !!i.children?.some(c => active(c.to))
const groupOpen = (i: NavItem) => toggled.value[i.label] ?? groupActive(i)
const toggle = (i: NavItem) => (toggled.value = { ...toggled.value, [i.label]: !groupOpen(i) })
watch(() => current.value.path, () => (toggled.value = {}))

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
          <li v-for="i in nav" :key="i.label">
            <NuxtLink
              v-if="i.to"
              :to="i.to"
              class="flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium transition"
              :class="active(i.to) ? 'bg-primary/10 text-primary' : 'text-muted hover:bg-surface-2 hover:text-fg'"
              :aria-current="active(i.to) ? 'page' : undefined"
            >
              <svg viewBox="0 0 24 24" class="size-[18px] shrink-0" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path :d="i.icon" /></svg>
              {{ i.label }}
            </NuxtLink>
            <template v-else>
              <button
                type="button"
                class="flex w-full items-center gap-3 rounded-lg px-3 py-2 text-left text-sm font-medium transition hover:bg-surface-2 hover:text-fg"
                :class="groupActive(i) ? 'text-fg' : 'text-muted'"
                :aria-expanded="groupOpen(i)"
                :aria-controls="`nav-${i.label}`"
                @click="toggle(i)"
              >
                <svg viewBox="0 0 24 24" class="size-[18px] shrink-0" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path :d="i.icon" /></svg>
                <span class="flex-1">{{ i.label }}</span>
                <svg viewBox="0 0 24 24" class="size-4 shrink-0 transition-transform" :class="groupOpen(i) && 'rotate-90'" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m9 18 6-6-6-6" /></svg>
              </button>
              <ul v-show="groupOpen(i)" :id="`nav-${i.label}`" class="mb-1 ml-[1.35rem] mt-0.5 flex flex-col gap-0.5 border-l border-border pl-3">
                <li v-for="c in i.children" :key="c.to">
                  <NuxtLink
                    :to="c.to"
                    class="block rounded-lg px-3 py-1.5 text-sm transition"
                    :class="active(c.to) ? 'bg-primary/10 font-medium text-primary' : 'text-muted hover:bg-surface-2 hover:text-fg'"
                    :aria-current="active(c.to) ? 'page' : undefined"
                  >{{ c.label }}</NuxtLink>
                </li>
              </ul>
            </template>
          </li>
        </ul>
      </nav>
      <div class="border-t border-border px-5 py-4">
        <p class="truncate text-sm font-medium">{{ auth.employee?.full_name }}</p>
        <p class="text-xs text-muted">{{ auth.employee?.role?.name ?? '' }}</p>
        <AppButton class="mt-3 w-full" size="sm" variant="secondary" @click="signOut">Sign out</AppButton>
      </div>
    </aside>

    <main class="min-w-0 px-4 py-6 sm:px-8">
      <div class="mx-auto max-w-6xl"><slot /></div>
    </main>
  </div>
</template>
